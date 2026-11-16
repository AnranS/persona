import XCTest

/// The call to Persona's screen (#9290, docs/design/DESIGN_SYSTEM.md "Call screen"), against scripts/mock/mock_hub.py
/// with MOCK_VOICE=1 (scripts/mock/voice_mock.py):
///   MOCK_VOICE=1 PORT=<port> python3 scripts/mock/mock_hub.py
///   TEST_RUNNER_PERSONA_VOICE_HUB=http://127.0.0.1:<port>
///   TEST_RUNNER_PERSONA_SHOTS=<dir>     screenshots, one file per state (call-connected.png, …), for the PR
/// The appearance is the simulator's (`simctl ui <udid> appearance light|dark`); a fresh mock and a fresh install per run.
/// The sample tests open each `-showCallSample` state (no system, no audio); `testACallGoesOnAwayFromTheApp` takes the
/// real path on the Simulator's stand-in for the system's calls (`SimulatedCallSystem`: the Simulator has no call
/// service), with the real socket to the mock's voice.
final class CallScreenTests: XCTestCase {
    private static let main = ["-app.tab", "assistant", "-tab", "assistant", "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001"]
    private static let agent = ["-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001"]

    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    private var hub: String {
        get throws {
            guard let hub = ProcessInfo.processInfo.environment["PERSONA_VOICE_HUB"] else {
                throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_VOICE=1 (PERSONA_VOICE_HUB)")
            }
            return hub
        }
    }

    private func launch(_ args: [String], largest: Bool = false) throws -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + (try hub).addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-AppleLanguages", "(zh-Hans)", "-UIPreferredContentSizeCategoryName",
                               largest ? "UICTContentSizeCategoryAccessibilityXXXL" : "UICTContentSizeCategoryL"] + args
        app.launchPinned()
        return app
    }

    /// The app's own picture, or the whole screen's (another app, the Home Screen, the Lock Screen).
    @MainActor private func shot(_ name: String, _ app: XCUIApplication? = nil) {
        let image = app?.screenshot() ?? XCUIScreen.main.screenshot()
        let attachment = XCTAttachment(screenshot: image)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        try? image.pngRepresentation.write(to: URL(fileURLWithPath: dir + "/\(name).png"))
    }

    private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any)[id].firstMatch
    }

    @MainActor private func sample(_ state: String, largest: Bool = false, args: [String] = main) throws -> XCUIApplication {
        let app = try launch(args + ["-showCallSample", state], largest: largest)
        XCTAssertTrue(element(app, "call-screen").waitForExistence(timeout: 30), "\(state): no call screen\n\(app.debugDescription)")
        Thread.sleep(forTimeInterval: 1.5) // the cover and the orb settle
        return app
    }

    // MARK: Each state, from the samples

    @MainActor func testTheStates() throws {
        let states: [(sample: String, name: String, check: (XCUIApplication) -> Void)] = [
            ("dialing", "call-calling", { app in
                XCTAssertTrue(app.staticTexts["正在呼叫…"].exists, "no 正在呼叫…\n\(app.debugDescription)")
            }),
            ("connected", "call-connected", { app in
                XCTAssertTrue(self.element(app, "call-duration").exists, "no duration")
                XCTAssertFalse(app.buttons["call-mute"].isSelected)
                XCTAssertTrue(app.buttons["call-speaker"].exists && !app.buttons["call-speaker"].isSelected, "the earpiece by default")
                XCTAssertFalse(app.buttons["call-resume"].exists)
            }),
            ("mutedSpeaker", "call-muted-speaker", { app in
                XCTAssertTrue(app.buttons["call-mute"].isSelected, "Mute is not on")
                XCTAssertTrue(app.buttons["call-speaker"].isSelected, "Speaker is not on")
            }),
            ("resume", "call-held-resume", { app in
                XCTAssertTrue(app.staticTexts["已保持"].exists, "no 已保持\n\(app.debugDescription)")
                XCTAssertTrue(app.buttons["call-resume"].exists, "no 恢复通话")
            }),
            ("reconnecting", "call-reconnecting", { app in
                XCTAssertTrue(app.staticTexts["重新连接中…"].exists, "no 重新连接中…\n\(app.debugDescription)")
            }),
            ("ended", "call-ended", { app in
                XCTAssertTrue(app.staticTexts["通话已结束 · 连不上 hub"].exists, "no ended note\n\(app.debugDescription)")
                XCTAssertFalse(app.buttons["call-end"].isEnabled, "End after the end")
            }),
            ("audioRoute", "call-audio-route", { app in
                XCTAssertTrue(self.element(app, "call-audio").exists, "no 音频 with AirPods there\n\(app.debugDescription)")
                XCTAssertFalse(app.buttons["call-speaker"].exists, "Speaker beside 音频")
            }),
        ]
        for (sample, name, check) in states {
            try XCTContext.runActivity(named: sample) { _ in
                let app = try self.sample(sample)
                check(app)
                shot(name, app)
                app.terminate()
            }
        }
    }

    /// The largest text size: the interface stops at xxxLarge, every label still on one line.
    @MainActor func testConnectedAtTheLargestTextSize() throws {
        let app = try sample("connected", largest: true)
        for id in ["call-mute", "call-speaker", "call-end", "call-minimise"] {
            XCTAssertTrue(app.buttons[id].isHittable, "\(id) is not on screen")
        }
        assertOnScreen(app, ["call-mute", "call-speaker", "call-end", "call-minimise"], "connected, largest")
        shot("call-connected-ax", app)
        app.terminate()
    }

    /// On hold with Resume at the largest text size, Persona's caption on two lines: every control inside the screen,
    /// frame and all, and the line (On Hold) and the caption in sight between Minimise and the controls. Run on a short
    /// phone too (an iPhone SE, 375 × 667), where the page was taller than the screen and Minimise and End went past its
    /// edges; there the orb gives way and the controls stay.
    @MainActor func testResumeAtTheLargestTextSize() throws {
        let app = try sample("resume", largest: true)
        let controls = ["call-mute", "call-speaker", "call-resume", "call-end"]
        assertOnScreen(app, ["call-minimise"] + controls, "resume, largest")
        let top = element(app, "call-minimise").frame.maxY - 0.5
        let bottom = controls.map { element(app, $0).frame.minY }.min()! + 0.5
        for id in ["call-state", "call-caption"] {
            let frame = element(app, id).frame
            XCTAssertTrue(element(app, id).exists && frame.minY >= top && frame.maxY <= bottom,
                          "\(id) at \(frame) is not between Minimise and the controls (\(top)…\(bottom))\n\(app.debugDescription)")
        }
        let screen = app.windows.firstMatch.frame
        shot("call-held-resume-ax-\(Int(screen.width))x\(Int(screen.height))", app)
        app.terminate()
    }

    /// Settings › Text Size reaches the call screen and the bar back to it, both drawn outside the app's root view: one
    /// step up (Large) grows the round controls as the text grows (body 17 → 19 pt), one step down (Small) shrinks them
    /// (UIFontMetrics' own curve: about 0.96 ×), and the bar grows with the text at the larger size. Without it both
    /// stayed the system size's (72 and 44 pt).
    @MainActor func testTheAppsTextSizeReachesTheCallScreenAndTheBar() throws {
        let large = try sample("connected", args: Self.main + ["-ui.textSize", "large"])
        XCTAssertEqual(large.buttons["call-end"].frame.width, Self.control * 19 / 17, accuracy: 1.5, "Large did not reach the call screen")
        large.buttons["call-minimise"].tap()
        let bar = large.buttons["call-return-bar"]
        XCTAssertTrue(bar.waitForExistence(timeout: 10), "no bar back to the call\n\(large.debugDescription)")
        XCTAssertGreaterThanOrEqual(bar.frame.height, Self.tapTarget * 19 / 17 - 1, "Large did not reach the bar")
        Thread.sleep(forTimeInterval: 1)
        shot("call-minimised-bar-large", large)
        large.terminate()

        let small = try sample("connected", args: Self.main + ["-ui.textSize", "small"])
        let width = small.buttons["call-end"].frame.width
        XCTAssertTrue(width < Self.control - 1.5 && width > Self.control * 15 / 17, "Small did not reach the call screen: \(width)")
        small.terminate()
    }

    /// `Theme.Size.callControl` and `tapTarget` at the default text size (the UI tests do not link the app's tokens).
    private static let control: CGFloat = 72
    private static let tapTarget: CGFloat = 44

    /// Each control inside the app's window, its whole frame: `isHittable` passes for one half off the screen.
    @MainActor private func assertOnScreen(_ app: XCUIApplication, _ ids: [String], _ what: String) {
        let screen = app.windows.firstMatch.frame.insetBy(dx: -0.5, dy: -0.5)
        for id in ids {
            let control = element(app, id)
            XCTAssertTrue(control.exists, "\(what): no \(id)\n\(app.debugDescription)")
            XCTAssertTrue(screen.contains(control.frame), "\(what): \(id) at \(control.frame) is outside the screen \(screen)")
        }
    }

    /// Minimise leaves the call for a bar at the top of the main conversation; a tap on it opens the call again.
    @MainActor func testMinimisedToTheBar() throws {
        let app = try sample("connected")
        app.buttons["call-minimise"].tap()
        let bar = app.buttons["call-return-bar"]
        XCTAssertTrue(bar.waitForExistence(timeout: 10), "no bar back to the call\n\(app.debugDescription)")
        XCTAssertTrue(element(app, "call-screen").waitForNonExistence(timeout: 5))
        Thread.sleep(forTimeInterval: 1.5)
        shot("call-minimised-bar", app)
        bar.tap()
        XCTAssertTrue(element(app, "call-screen").waitForExistence(timeout: 10), "the bar did not open the call")
        app.terminate()
    }

    /// The main conversation's + panel: Call Persona right after Voice Chat; an Agent's has none.
    @MainActor func testCallPersonaInTheMainConversationsPlus() throws {
        let app = try launch(Self.main)
        try openMore(app)
        XCTAssertTrue(app.buttons["composer-menu-voice"].exists, "Voice Chat is gone")
        let call = app.buttons["composer-menu-call"]
        XCTAssertTrue(call.exists, "no Call Persona\n\(app.debugDescription)")
        XCTAssertGreaterThan(call.frame.minY, app.buttons["composer-menu-voice"].frame.minY, "Call Persona before Voice Chat")
        shot("main-plus-call-row", app)
        app.terminate()

        let agent = try launch(Self.agent)
        try openMore(agent)
        XCTAssertTrue(agent.buttons["composer-menu-voice"].exists)
        XCTAssertFalse(agent.buttons["composer-menu-call"].exists, "Call Persona in an Agent's chat")
        agent.terminate()
    }

    @MainActor private func openMore(_ app: XCUIApplication) throws {
        let plus = app.descendants(matching: .any).matching(identifier: "shell-composer-add").firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 40), "no + button:\n\(app.debugDescription)")
        sleep(2)
        plus.tap()
        let more = app.buttons["attach-tab-more"]
        XCTAssertTrue(more.waitForExistence(timeout: 10), "no panel:\n\(app.debugDescription)")
        more.tap()
        XCTAssertTrue(app.buttons["composer-menu-voice"].waitForExistence(timeout: 10), "no More:\n\(app.debugDescription)")
        sleep(1)
    }

    /// The urgent call's page has the call screen's Mute and Speaker (off: the earpiece by default); Mute on, it fills
    /// and the line says so.
    @MainActor func testTheUrgentCallsMute() throws {
        let app = try launch(Self.main + ["-showUrgentCallSample", "YES"])
        let mute = app.buttons["urgentCall.mute"]
        XCTAssertTrue(mute.waitForExistence(timeout: 30), "no Mute on the urgent call\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons["urgentCall.hangUp"].exists, "no 挂断")
        XCTAssertTrue(app.buttons["urgentCall.speaker"].exists && !app.buttons["urgentCall.speaker"].isSelected,
                      "no 扬声器 off by default on the urgent call")
        mute.tap()
        XCTAssertTrue(mute.isSelected, "Mute did not turn on")
        XCTAssertTrue(app.staticTexts["已静音"].waitForExistence(timeout: 5), "the line does not say 已静音\n\(app.debugDescription)")
        Thread.sleep(forTimeInterval: 1)
        shot("urgent-call-mute", app)
        app.terminate()
    }

    // MARK: The real path

    private func log(_ hub: String) throws -> [String: Any] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/voice2")!)
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    private func audio(_ hub: String) throws -> Int { try XCTUnwrap(log(hub)["audio"] as? Int) }

    /// + › Call Persona; the call connects (its time runs); minimised, Home and another app for 15 s: the audio keeps
    /// going up to the mock; the Home Screen and the Lock Screen photographed (the Simulator has no call service, so
    /// neither shows the call); back in the app the call screen is up and still connected, and the mock's start was a
    /// system call's (`surface: call`); End ends it.
    @MainActor func testACallGoesOnAwayFromTheApp() throws {
        let hub = try hub
        let app = try launch(Self.main)
        try openMore(app)
        app.buttons["composer-menu-call"].tap()
        // A fresh install asks for the microphone in the app first (the call asks before it starts).
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.buttons.matching(NSPredicate(format: "label IN %@", ["Allow", "允许"])).firstMatch
        if allow.waitForExistence(timeout: 5) { allow.tap() }
        XCTAssertTrue(element(app, "call-screen").waitForExistence(timeout: 15), "no call screen\n\(app.debugDescription)")
        XCTAssertTrue(element(app, "call-duration").waitForExistence(timeout: 30), "the call never connected\n\(app.debugDescription)")
        Thread.sleep(forTimeInterval: 3)
        shot("real-call-connected", app)
        let starts = try XCTUnwrap(log(hub)["starts"] as? [[String: Any]])
        XCTAssertEqual(starts.last?["surface"] as? String, "call", "the start was not a system call's: \(starts)")

        app.buttons["call-minimise"].tap()
        XCTAssertTrue(app.buttons["call-return-bar"].waitForExistence(timeout: 10), "no bar back to the call")
        // The main conversation under it: the voice bar in the composer's place, with Show Call beside Stop.
        XCTAssertTrue(app.buttons["voice-expand"].waitForExistence(timeout: 10), "no Show Call on the voice bar\n\(app.debugDescription)")
        Thread.sleep(forTimeInterval: 1)
        shot("real-call-minimised", app)
        let before = try audio(hub)
        XCUIDevice.shared.press(.home)
        sleep(2)
        shot("lock-or-island-home")
        let settings = XCUIApplication(bundleIdentifier: "com.apple.Preferences")
        settings.launch()
        sleep(15)
        shot("lock-or-island-other-app")
        let away = try audio(hub)
        XCTAssertGreaterThan(away, before + 20, "the audio stopped while away (\(before) → \(away))")

        // Lock and wake to the Lock Screen, then unlock (a locked simulator hangs the next run).
        XCUIDevice.shared.perform(NSSelectorFromString("pressLockButton"))
        sleep(2)
        XCUIDevice.shared.perform(NSSelectorFromString("pressLockButton"))
        sleep(3)
        shot("lock-or-island-lock")
        let bottom = springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.99))
        bottom.press(forDuration: 0.1, thenDragTo: springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3)))
        sleep(2)
        let locked = try audio(hub)
        XCTAssertGreaterThan(locked, away, "the audio stopped while locked (\(away) → \(locked))")

        // Back in the app (as from the Dynamic Island or the status bar's call): the call screen, still connected.
        app.activate()
        XCTAssertTrue(element(app, "call-screen").waitForExistence(timeout: 10), "the call screen did not come back\n\(app.debugDescription)")
        XCTAssertTrue(element(app, "call-duration").exists, "the call is not connected after coming back")
        let closes = try XCTUnwrap(log(hub)["closes"] as? Int)
        XCTAssertEqual(closes, 0, "the voice's socket closed while away")
        shot("real-call-back", app)

        app.buttons["call-end"].tap()
        XCTAssertTrue(app.staticTexts["通话已结束"].waitForExistence(timeout: 5), "no 通话已结束\n\(app.debugDescription)")
        shot("real-call-ended", app)
        XCTAssertTrue(element(app, "call-screen").waitForNonExistence(timeout: 10), "the ended call stayed")
        XCTAssertFalse(app.buttons["call-return-bar"].exists, "a bar for an ended call")
        let after = try audio(hub)
        sleep(3)
        XCTAssertLessThanOrEqual(try audio(hub) - after, 5, "the audio went on after End")
        app.terminate()
    }
}
