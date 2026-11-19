import VisionKit
import XCTest

/// Pairing can choose a QR photo even where the camera scanner is unsupported or unavailable (#11057). The action uses
/// the existing localized Choose from Photos copy and opens the system picker without a full-photo-library prompt.
final class PairPhotoPickerUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor func testChooseFromPhotosIsAvailableWithoutCameraSupport() throws {
        guard !(DataScannerViewController.isSupported && DataScannerViewController.isAvailable) else {
            throw XCTSkip("this case covers devices where the camera scanner is unsupported or unavailable")
        }
        let app = XCUIApplication()
        app.launchArguments = ["-openWelcome", "YES", "-pairURL", "", "-ui.scheme", "light"]
        app.launchPinned()
        defer { app.terminate() }

        XCTAssertTrue(app.buttons["welcome.pair"].waitForExistence(timeout: 10), "no welcome page")
        app.buttons["welcome.pair"].tap()
        let scanOrToken = app.buttons["扫码或其他配对方式"]
        XCTAssertTrue(scanOrToken.waitForExistence(timeout: 10), "no scan-or-token pairing method")
        scanOrToken.tap()

        let choose = app.buttons["pair.choosePhoto"]
        XCTAssertTrue(choose.waitForExistence(timeout: 10),
                      "photo QR selection disappeared with the camera scanner:\n\(app.debugDescription)")
        XCTAssertTrue(["Choose from Photos", "从照片选取", "写真から選択"].contains(choose.label),
                      "the new action does not use the localized Choose from Photos copy: \(choose.label)")
        choose.tap()

        let covered = expectation(for: NSPredicate(format: "hittable == false"), evaluatedWith: choose)
        XCTAssertEqual(XCTWaiter.wait(for: [covered], timeout: 10), .completed,
                       "Choose from Photos did not open a picker:\n\(app.debugDescription)")
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let fullAccess = springboard.buttons.matching(NSPredicate(format: "label IN %@",
            ["Allow Full Access", "允许完全访问", "フルアクセスを許可"])).firstMatch
        XCTAssertFalse(fullAccess.exists, "the system PhotosPicker should not request full photo-library access")
    }
}
