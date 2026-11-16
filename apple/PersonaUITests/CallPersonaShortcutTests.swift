import XCTest

/// Call Persona and Talk to Persona (no car) as the system runs them (#9290, docs/apple/siri.md「Calling Persona」), against
/// scripts/mock/mock_hub.py with MOCK_VOICE=1 (scripts/mock/voice_mock.py):
///   MOCK_VOICE=1 PORT=<port> python3 scripts/mock/mock_hub.py
///   TEST_RUNNER_PERSONA_VOICE_HUB=http://127.0.0.1:<port>
///   TEST_RUNNER_PERSONA_SHOTS=<dir>   screenshots of Spotlight and the call
/// Persona paired and in the background, each App Shortcut run from Spotlight's Top Hit: the app comes to the front and the
/// call to Persona starts on the main conversation, which the mock sees as a start frame with `surface: "call"`
/// (`GET /mock/voice2`). A simulator without the microphone granted shows the prompt: it is allowed.
/// Not Siri: the Simulator's Siri runs no App Shortcut ("Talk to Persona": "I didn't get that"), and takes "Call
/// Persona" or "Start a call with Persona" as a phone call of its own ("Sorry, I can't make a phone call"). A device
/// check (docs/apple/siri.md, "Verification on a phone").
final class CallPersonaShortcutTests: XCTestCase {
    private var hub: String {
        get throws {
            guard let hub = ProcessInfo.processInfo.environment["PERSONA_VOICE_HUB"] else {
                throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_VOICE=1 (PERSONA_VOICE_HUB)")
            }
            return hub
        }
    }

    @MainActor private var springboard: XCUIApplication { XCUIApplication(bundleIdentifier: "com.apple.springboard") }

    private func starts(_ hub: String) throws -> [[String: Any]] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/voice2")!)
        let log = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        return log["starts"] as? [[String: Any]] ?? []
    }

    @MainActor private func shot(_ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        try? XCUIScreen.main.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir + "/call-persona-\(name).png"))
    }

    /// Persona paired with the mock, its main conversation read, then in the background with no call on. Returns how many
    /// starts the mock had seen.
    @MainActor private func personaInTheBackground(hub: String) throws -> (XCUIApplication, Int) {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-AppleLanguages", "(zh-Hans)"]
        app.launchPinned()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        Thread.sleep(forTimeInterval: 6) // the hub and its main conversation
        let before = try starts(hub).count
        XCUIDevice.shared.press(.home)
        XCTAssertTrue(springboard.wait(for: .runningForeground, timeout: 10), "no home screen")
        Thread.sleep(forTimeInterval: 1)
        return (app, before)
    }

    /// The call to Persona the mock saw after `before` starts, waiting up to `seconds` (allowing the microphone).
    @MainActor private func call(_ hub: String, after before: Int, seconds: Double) throws -> [String: Any]? {
        let allow = springboard.buttons.matching(NSPredicate(format: "label IN %@", ["Allow", "允许"])).firstMatch
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if allow.exists { allow.tap() }
            if let call = try starts(hub).dropFirst(before).last(where: { $0["surface"] as? String == "call" }) { return call }
            Thread.sleep(forTimeInterval: 1)
        }
        return nil
    }

    /// `query` typed into Spotlight with Persona in the background and its Top Hit run (Return; the field reads
    /// "<query> — Open"), as Shortcuts and the Action button run an App Shortcut: the system brings Persona to the front
    /// and the call starts.
    @MainActor private func spotlightCalls(_ query: String, shots name: String) throws {
        let hub = try hub
        let (app, before) = try personaInTheBackground(hub: hub)
        springboard.swipeDown() // Spotlight, its own app, with the keyboard up
        let spotlight = XCUIApplication(bundleIdentifier: "com.apple.Spotlight")
        XCTAssertTrue(spotlight.wait(for: .runningForeground, timeout: 10), "no Spotlight")
        Thread.sleep(forTimeInterval: 1)
        spotlight.typeText(query)
        Thread.sleep(forTimeInterval: 3)
        shot(name + "-spotlight")
        print("call-persona: Spotlight shows\n\(spotlight.debugDescription)")
        spotlight.typeText("\n")
        let started = try call(hub, after: before, seconds: 40)
        shot(name + "-call")
        let seen = try starts(hub)
        let call = try XCTUnwrap(started, "no call to Persona started; starts: \(seen)")
        XCTAssertEqual(app.state, .runningForeground, "Persona is not in front for the call")
        XCTAssertEqual(call["surface"] as? String, "call")
        XCTAssertEqual(call["protocol"] as? Int, 2)
        app.terminate()
    }

    /// Call Persona (`CallPersonaIntent`, in front from the start).
    @MainActor func testSpotlightCallsPersona() throws {
        try spotlightCalls("Call Persona", shots: "call")
    }

    /// Talk to Persona with no car connected (`TalkToPersonaIntent`): run in the background, it comes to the front
    /// (`continueInForeground`) and calls Persona instead of saying to connect CarPlay.
    @MainActor func testSpotlightTalkToPersonaCallsWithoutACar() throws {
        try spotlightCalls("Talk to Persona", shots: "talk")
    }
}
