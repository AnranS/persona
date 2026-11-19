import XCTest

/// Issue #9612 item 4: a device that cannot receive pushes says so on Persona's main screen, not only in Settings.
/// Against scripts/mock/mock_hub.py (PERSONA_MOCK_HUB). `-showPushHealth unreachable` needs no APNs token; `direct` is
/// the healthy control. PERSONA_SHOTS optionally writes the light/dark and largest-text captures outside the repository.
final class PushHealthNoticeUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    override func tearDown() {
        app?.terminate()
        super.tearDown()
    }

    @MainActor private func launch(_ sample: String, look: String = "light", largest: Bool = false) {
        app?.terminate()
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant",
                               "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001",
                               "-showPushHealth", sample, "-ui.scheme", look]
            + (largest ? ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] : [])
        app.launchPinned()

        // Answer only the system's notification question. A previously authorized simulator shows no alert.
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.buttons.matching(NSPredicate(format: "label IN %@", ["允许", "Allow"])).firstMatch
        if allow.waitForExistence(timeout: 2) { allow.tap() }

        // Prove the fixture reached Persona's main screen before blaming a missing notice on the implementation.
        let composer = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 20), "Persona's main screen did not open:\n\(app.debugDescription)")
    }

    @MainActor private var notice: XCUIElement {
        app.descendants(matching: .any)["pushHealth.notice"].firstMatch
    }

    @MainActor private func requireNotice() -> XCUIElement {
        XCTAssertTrue(notice.waitForExistence(timeout: 10),
                      "an unreachable push sample has no notice on Persona's main screen:\n\(app.debugDescription)")
        XCTAssertTrue(notice.isHittable, "the push-health notice exists but is not visible")
        return notice
    }

    @MainActor func testUnhealthySampleShowsMainScreenNoticeAndHealthySampleDoesNot() {
        launch("unreachable")
        _ = requireNotice()

        launch("direct")
        XCTAssertFalse(notice.waitForExistence(timeout: 2), "a healthy direct push route must not show the notice")
    }

    @MainActor func testNoticeUsesPlainLanguageAndOneStatusSpecificRepairAction() {
        launch("unreachable")
        let notice = requireNotice()
        let primary = notice.staticTexts.firstMatch.label
        XCTAssertTrue(primary.contains("通知") && primary.contains("设备"),
                      "the primary message must say which everyday capability is affected: \(primary)")
        XCTAssertTrue(primary.contains("检查网络"), "the primary message must name this status's repair: \(primary)")
        for detail in ["HTTP", "relay", "中继", "credential", "凭据", "APNs", "apns.json", "403", "502",
                       "The request timed out", "topic not served"] {
            XCTAssertFalse(primary.localizedCaseInsensitiveContains(detail),
                           "engineering detail '\(detail)' is the primary message: \(primary)")
        }

        XCTAssertEqual(notice.buttons.count, 1, "the notice must offer exactly one repair action")
        let action = notice.buttons.firstMatch
        XCTAssertTrue(action.label.contains("通知设置") || action.label.contains("Notification Settings"),
                      "the action must name its actual destination: \(action.label)")
        action.tap()

        // The action uses the existing repair surface; no physical APNs registration or external App Store is involved.
        let repair = app.descendants(matching: .any)["notifications.backgroundPush"].firstMatch
        XCTAssertTrue(repair.waitForExistence(timeout: 10),
                      "the notice's action did not open Settings › Notifications:\n\(app.debugDescription)")
        XCTAssertTrue(repair.label.contains("检查网络") && repair.label.contains("下次打开 Persona"),
                      "the existing status-specific repair was not kept: \(repair.label)")
    }

    @MainActor func testNoticeCapturesInLightAndDarkAtDefaultAndLargestText() {
        for largest in [false, true] {
            for look in ["light", "dark"] {
                launch("unreachable", look: look, largest: largest)
                let notice = requireNotice()
                XCTAssertTrue(notice.buttons.firstMatch.isHittable,
                              "the repair action is not visible at \(look), largest=\(largest)")
                shot(look: look, largest: largest)
            }
        }
    }

    @MainActor private func shot(look: String, largest: Bool) {
        let image = app.screenshot()
        let name = "push-health-notice-\(look)-\(largest ? "largest" : "default")-\(UITestStyle.name)"
        let attachment = XCTAttachment(screenshot: image)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? image.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: name + ".png"))
    }
}
