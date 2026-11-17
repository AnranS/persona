import Foundation
import XCTest

/// UI version 10's graph on the mock's made-up 200-node Work page and its compact overview card
/// (`MOCK_EXTENSIONS=1 MOCK_EXTENSION_GRAPH=1`, `TEST_RUNNER_PERSONA_GRAPH_HUB=http://127.0.0.1:<port>`).
/// Covers zoom, folding, full screen, a node's row-style push, and the still preview opening the same canvas.
final class ExtensionGraphUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var pad: Bool { UIDevice.current.userInterfaceIdiom == .pad }
    private var scheme: String { env["PERSONA_GRAPH_SCHEME"] ?? "light" }
    private let workEntry = "sidebar-extension-example-work-work"

    @MainActor private func launch(tab: String = "assistant") throws -> XCUIApplication {
        guard let hub = env["PERSONA_GRAPH_HUB"] else {
            throw XCTSkip("needs mock_hub.py with MOCK_EXTENSIONS=1 MOCK_EXTENSION_GRAPH=1 (PERSONA_GRAPH_HUB)")
        }
        continueAfterFailure = false
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", tab, "-dev.openChat", "", "-demo.on", "NO",
                               "-ui.scheme", scheme]
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) {
            springboard.buttons[label].tap()
        }
        return app
    }

    @MainActor private func openWork(_ app: XCUIApplication) {
        let row = app.descendants(matching: .any)[workEntry]
        if !row.waitForExistence(timeout: 10) {
            let sidebar = app.buttons["assistant-sidebar-button"]
            XCTAssertTrue(sidebar.waitForExistence(timeout: 10), app.debugDescription)
            sidebar.tap()
        }
        XCTAssertTrue(row.waitForExistence(timeout: 10), app.debugDescription)
        row.tap()
        XCTAssertTrue(app.descendants(matching: .any)["extension-ui-ready"].waitForExistence(timeout: 20),
                      app.debugDescription)
        XCTAssertTrue(app.descendants(matching: .any)["extension-graph"].waitForExistence(timeout: 20),
                      app.debugDescription)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let base = URL(fileURLWithPath: dir).appending(path: "graph-\(pad ? "ipad" : "iphone")-\(name)-\(scheme).png")
        try? app.screenshot().pngRepresentation.write(to: base)
    }

    @MainActor func testZoomFoldFullScreenAndNodePush() throws {
        let app = try launch()
        openWork(app)
        let zoomOut = app.buttons["extension-graph-zoom-out"]
        XCTAssertTrue(zoomOut.waitForExistence(timeout: 10), app.debugDescription)
        zoomOut.tap()
        app.buttons["extension-graph-zoom-in"].tap()

        // Fold the visible part around the selected step, then restore it. The control is independent of the node.
        let fold = app.buttons["extension-graph-fold-p3.s3"]
        XCTAssertTrue(fold.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(fold.label, "Fold")
        fold.tap()
        XCTAssertTrue(fold.label.hasPrefix("Show "), fold.label)
        fold.tap()
        XCTAssertEqual(fold.label, "Fold")

        app.buttons["extension-graph-full-screen"].tap()
        XCTAssertTrue(app.buttons["extension-graph-close"].waitForExistence(timeout: 10), app.debugDescription)
        shot(app, "full-screen")
        let selected = app.descendants(matching: .any)["extension-graph-node-p2.s3"].firstMatch
        XCTAssertTrue(selected.waitForExistence(timeout: 10), app.debugDescription)
        selected.tap()
        let pushed = app.staticTexts["This page stands for a node's own page: its question, its result and its steps."]
        XCTAssertTrue(pushed.waitForExistence(timeout: 15), app.debugDescription)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(app.buttons["extension-graph-full-screen"].waitForExistence(timeout: 15), app.debugDescription)
        shot(app, "page")
        app.buttons["extension-graph-fit"].tap()
    }

    @MainActor func testCompactPreviewOpensFullScreen() throws {
        let app = try launch(tab: "settings")
        if !app.descendants(matching: .any)["settings-list"].waitForExistence(timeout: 5) {
            let sidebar = app.buttons["assistant-sidebar-button"]
            XCTAssertTrue(sidebar.waitForExistence(timeout: 10), app.debugDescription)
            sidebar.tap()
            let settings = app.drawerSettingsButton
            XCTAssertTrue(settings.waitForExistence(timeout: 10), app.debugDescription)
            settings.tap()
        }
        XCTAssertTrue(app.descendants(matching: .any)["settings-list"].waitForExistence(timeout: 15),
                      app.debugDescription)
        let plugins = app.descendants(matching: .any)["settings-plugins"]
        XCTAssertTrue(plugins.waitForExistence(timeout: 10), app.debugDescription)
        plugins.tap()
        let card = app.descendants(matching: .any)["plugin-card-example-work-work-card"]
        let preview = app.descendants(matching: .any)["extension-graph-preview"]
        let settingsList = app.descendants(matching: .any)["settings-list"]
        for _ in 0..<10 where !card.waitForExistence(timeout: 2) { settingsList.swipeUp(velocity: .fast) }
        XCTAssertTrue(card.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(preview.waitForExistence(timeout: 15), app.debugDescription)
        preview.tap()
        XCTAssertTrue(app.buttons["extension-graph-close"].waitForExistence(timeout: 15), app.debugDescription)
        shot(app, "preview-full-screen")
        app.buttons["extension-graph-close"].tap()
        XCTAssertTrue(preview.waitForExistence(timeout: 10), app.debugDescription)
    }
}
