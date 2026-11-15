import Foundation
import Testing
import UIKit
@testable import Persona
import PersonaWire

/// Clipboard sharing with a computer (#3064): wire decoding, Send / Get / the switch against a FakeHub, the hub's
/// refusals in words, and the once-only paste hint.
@MainActor struct ClipboardModelTests {
    /// A pasteboard in memory; `refuses`: the user tapped Don't Allow (the read comes back empty).
    @MainActor final class Board {
        var content: ClipboardContent?
        var refuses = false
        var reads = 0
        var pasteboard: PhonePasteboard {
            PhonePasteboard(read: { [self] in
                reads += 1
                return refuses ? nil : content
            }, write: { [self] in content = $0 }, hasContent: { [self] in content != nil })
        }
    }

    @MainActor final class Hints {
        var dismissed = false
        func store(applies: Bool = true) -> ClipboardHints {
            ClipboardHints(applies: applies, dismissed: { [self] in dismissed }, dismiss: { [self] in dismissed = true })
        }
    }

    /// Holds each decode until the test chooses its completion order.
    @MainActor final class ControlledDecoder {
        struct Request {
            let data: Data
            let maxPixel: CGFloat
            let continuation: CheckedContinuation<ExtensionPicture.Decoded?, Never>
        }
        private(set) var requests: [Request] = []

        func decode(_ data: Data, maxPixel: CGFloat) async -> ExtensionPicture.Decoded? {
            await withCheckedContinuation { requests.append(Request(data: data, maxPixel: maxPixel, continuation: $0)) }
        }

        func wait(for count: Int) async {
            while requests.count < count { await Task.yield() }
        }

        func finish(_ index: Int, image: UIImage) {
            let request = requests[index]
            request.continuation.resume(returning: .init(image: image, data: request.data, cost: request.data.count))
        }
    }

    private func picture(width: Int, height: Int, color: UIColor = .red) -> UIImage {
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        return UIGraphicsImageRenderer(size: CGSize(width: width, height: height), format: format).image { context in
            color.setFill()
            context.fill(CGRect(x: 0, y: 0, width: width, height: height))
        }
    }

    private func png(width: Int, height: Int) throws -> Data {
        try #require(picture(width: width, height: height).pngData())
    }

    private func imageContent(_ data: Data) -> ClipboardContent {
        .image(ClipboardImage(mime: "image/png", bytes: data))
    }

    private func model(_ hub: FakeHub, _ board: Board, hints: ClipboardHints = Hints().store(),
                       updated: @escaping (MachineClipboard) -> Void = { _ in },
                       decoder: ClipboardModel.Decoder? = nil) -> ClipboardModel {
        ClipboardModel(deps: .init(api: { hub.client }, pasteboard: board.pasteboard, updated: updated, hints: hints),
                       decoder: decoder)
    }

    @Test func decodesMachinesAndContent() throws {
        let old = try JSON.decoder.decode(Machine.self, from: Data(#"{"id":"mac","name":"Mac","kind":"local","online":true}"#.utf8))
        #expect(old.clipboard == nil)
        let m = try JSON.decoder.decode(Machine.self, from: Data(#"{"id":"mac","name":"Mac","kind":"local","online":true,"clipboard":{"supported":false,"enabled":false,"reason":"没有图形会话"}}"#.utf8))
        #expect(m.clipboard == MachineClipboard(supported: false, enabled: false, reason: "没有图形会话"))
        #expect(m.clipboard?.usable == false)
        #expect(try JSON.decoder.decode(Machine.self, from: JSON.encoder.encode(m)) == m)

        let image = try JSON.decoder.decode(ClipboardContent.self, from: Data(#"{"kind":"image","image":{"mime":"image/png","data":"AAEC"}}"#.utf8))
        #expect(image.kind == .image && image.image?.bytes == Data([0, 1, 2]))
        let future = try JSON.decoder.decode(ClipboardContent.self, from: Data(#"{"kind":"rtf","text":"hi"}"#.utf8))
        #expect(future.kind == .text && future.text == "hi")
        #expect(try JSON.decoder.decode(ClipboardContent.self, from: Data(#"{"kind":"concealed"}"#.utf8)).kind == .concealed)
    }

    @Test func sendsTextAndShowsTheHintOnce() async throws {
        let hub = FakeHub()
        hub.on("PUT", "/api/v1/machines/mac/clipboard") { req in
            let body = try JSON.decoder.decode([String: String].self, from: #require(req.body))
            #expect(body == ["text": "https://example.com"])
            return Data()
        }
        let board = Board()
        board.content = .text("https://example.com")
        let hints = Hints()
        let m = model(hub, board, hints: hints.store())
        #expect(await m.sendPasteboard(machineID: "mac") == .sent(.text))
        #expect(m.showsPasteHint && m.busy == nil)
        m.dismissPasteHint()
        #expect(!m.showsPasteHint && hints.dismissed)
        #expect(await m.sendPasteboard(machineID: "mac") == .sent(.text))
        #expect(!m.showsPasteHint, "dismissed for good")
        #expect(hub.sent("PUT", "/api/v1/machines/mac/clipboard").count == 2)
    }

    @Test func refusedPasteSendsNothingAndGivesNoHint() async {
        let hub = FakeHub()
        let board = Board()
        board.content = .text("secret")
        board.refuses = true
        let m = model(hub, board)
        #expect(await m.sendPasteboard(machineID: "mac") == .nothingToSend)
        #expect(!m.showsPasteHint && hub.requests.isEmpty)
        #expect(m.outcome?.isFailure == true)
    }

    @Test func pasteButtonSendsWithoutReadingAndWithoutHint() async throws {
        let hub = FakeHub()
        hub.on("PUT", "/api/v1/machines/mac/clipboard") { req in
            let body = try JSON.decoder.decode(JSONValue.self, from: #require(req.body))
            #expect(body["image"]?["mime"]?.string == "image/png" && body["text"] == nil)
            return Data()
        }
        let board = Board()
        let m = model(hub, board)
        #expect(await m.send(.image(ClipboardImage(mime: "image/png", bytes: Data([1, 2]))), machineID: "mac") == .sent(.image))
        #expect(board.reads == 0 && !m.showsPasteHint)
        #expect(await m.send(nil, machineID: "mac") == .nothingToSend)
    }

    @Test func fetchWritesThePhoneAndKeepsThePreview() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/mac/clipboard", json: #"{"kind":"text","text":"from the Mac"}"#)
        let board = Board()
        let m = model(hub, board)
        #expect(await m.fetch(machineID: "mac") == .fetched(.text))
        #expect(board.content == .text("from the Mac") && m.fetched == .text("from the Mac"))
        #expect(board.reads == 0, "getting never reads the phone's clipboard")

        hub.on("GET", "/api/v1/machines/mac/clipboard", json: #"{"kind":"concealed"}"#)
        #expect(await m.fetch(machineID: "mac") == .remoteConcealed)
        hub.on("GET", "/api/v1/machines/mac/clipboard", json: #"{"kind":"empty"}"#)
        #expect(await m.fetch(machineID: "mac") == .remoteEmpty)
        #expect(board.content == .text("from the Mac"), "nothing overwritten")
    }

    @Test func fetchDownsamplesLargeImagesWithoutUpscalingSmallOnes() async throws {
        let hub = FakeHub()
        let board = Board()
        let m = model(hub, board)
        let large = imageContent(try png(width: 400, height: 200))
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in try JSON.encoder.encode(large) }

        #expect(await m.fetch(machineID: "mac", maxPixel: 100) == .fetched(.image))
        #expect(m.fetched == large && board.content == large, "the original goes to the phone and stays with the preview")
        #expect(m.fetchedImage?.cgImage?.width == 100 && m.fetchedImage?.cgImage?.height == 50)

        let small = imageContent(try png(width: 40, height: 20))
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in try JSON.encoder.encode(small) }
        #expect(await m.fetch(machineID: "mac", maxPixel: 100) == .fetched(.image))
        #expect(m.fetched == small && m.fetchedImage?.cgImage?.width == 40 && m.fetchedImage?.cgImage?.height == 20)
    }

    @Test func aLargerPreviewIsDecodedAgainFromTheOriginalWithoutRefetching() async throws {
        let hub = FakeHub()
        let m = model(hub, Board())
        let content = imageContent(try png(width: 400, height: 200))
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in try JSON.encoder.encode(content) }
        #expect(await m.fetch(machineID: "mac", maxPixel: 100) == .fetched(.image))
        #expect(m.fetchedImage?.cgImage?.width == 100)

        await m.resizePreview(maxPixel: 200)
        #expect(m.fetched == content && m.fetchedImage?.cgImage?.width == 200 && m.fetchedImage?.cgImage?.height == 100)
        await m.resizePreview(maxPixel: 100)
        #expect(m.fetchedImage?.cgImage?.width == 200, "a smaller window keeps the sharper image")
        #expect(hub.sent("GET", "/api/v1/machines/mac/clipboard").count == 1)
    }

    @Test(.timeLimit(.minutes(1))) func resizingTheOldPreviewDoesNotCancelANewerFetch() async throws {
        let hub = FakeHub()
        let board = Board()
        let decoder = ControlledDecoder()
        let m = model(hub, board, decoder: { data, maxPixel in await decoder.decode(data, maxPixel: maxPixel) })
        let old = imageContent(try png(width: 40, height: 20))
        let new = imageContent(try png(width: 60, height: 30))
        var answers = [old, new]
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in try JSON.encoder.encode(answers.removeFirst()) }

        let initial = Task { await m.fetch(machineID: "mac", maxPixel: 100) }
        await decoder.wait(for: 1)
        decoder.finish(0, image: picture(width: 40, height: 20))
        #expect(await initial.value == .fetched(.image))

        let newFetch = Task { await m.fetch(machineID: "mac", maxPixel: 100) }
        await decoder.wait(for: 2)
        let resize = Task { await m.resizePreview(maxPixel: 200) }
        await decoder.wait(for: 3)

        let newImage = picture(width: 60, height: 30, color: .blue)
        decoder.finish(1, image: newImage)
        #expect(await newFetch.value == .fetched(.image), "a resize of the old preview never supersedes the user's fetch")
        #expect(m.fetched == new && m.fetchedImage === newImage && board.content == new)

        decoder.finish(2, image: picture(width: 80, height: 40, color: .green))
        await resize.value
        #expect(m.fetched == new && m.fetchedImage === newImage, "the old resize cannot overwrite the new fetch either")
    }

    @Test(.timeLimit(.minutes(1))) func aFailedFetchDoesNotCancelTheCurrentPreviewResize() async throws {
        let hub = FakeHub()
        let decoder = ControlledDecoder()
        let m = model(hub, Board(), decoder: { data, maxPixel in await decoder.decode(data, maxPixel: maxPixel) })
        let content = imageContent(try png(width: 40, height: 20))
        var reads = 0
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in
            reads += 1
            if reads == 1 { return try JSON.encoder.encode(content) }
            throw APIError(status: 503, message: "offline")
        }

        let initial = Task { await m.fetch(machineID: "mac", maxPixel: 100) }
        await decoder.wait(for: 1)
        decoder.finish(0, image: picture(width: 40, height: 20))
        #expect(await initial.value == .fetched(.image))

        let resize = Task { await m.resizePreview(maxPixel: 200) }
        await decoder.wait(for: 2)
        #expect(await m.fetch(machineID: "mac", maxPixel: 100) == .failed(.offline))
        let larger = picture(width: 80, height: 40, color: .green)
        decoder.finish(1, image: larger)
        await resize.value
        #expect(m.fetched == content && m.fetchedImage === larger)
    }

    @Test(.timeLimit(.minutes(1))) func cancelledResizeTasksShareOneWorkerAndKeepTheNewestSize() async throws {
        let hub = FakeHub()
        let decoder = ControlledDecoder()
        let m = model(hub, Board(), decoder: { data, maxPixel in await decoder.decode(data, maxPixel: maxPixel) })
        let content = imageContent(try png(width: 400, height: 200))
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in try JSON.encoder.encode(content) }

        let initial = Task { await m.fetch(machineID: "mac", maxPixel: 100) }
        await decoder.wait(for: 1)
        let initialImage = picture(width: 40, height: 20)
        decoder.finish(0, image: initialImage)
        #expect(await initial.value == .fetched(.image))

        let cancelled = Task { await m.resizePreview(maxPixel: 200) }
        await decoder.wait(for: 2)
        let newest = Task { await m.resizePreview(maxPixel: 400) }
        await Task.yield()
        cancelled.cancel() // SwiftUI cancels the old task when the geometry changes again.
        await Task.yield()
        #expect(decoder.requests.count == 2, "the second size waits for the in-flight decode instead of overlapping it")

        decoder.finish(1, image: picture(width: 80, height: 40, color: .green))
        await decoder.wait(for: 3)
        #expect(decoder.requests.map(\.maxPixel) == [100, 200, 400])
        #expect(m.fetchedImage === initialImage, "the superseded size is not published")

        let larger = picture(width: 160, height: 80, color: .blue)
        decoder.finish(2, image: larger)
        await cancelled.value
        await newest.value
        #expect(m.fetched == content && m.fetchedImage === larger)
        #expect(decoder.requests.count == 3, "the cancelled caller did not cancel the shared worker")
    }

    @Test(.timeLimit(.minutes(1))) func newerFetchWinsWhenTheOlderDecodeFinishesLater() async throws {
        let hub = FakeHub()
        let board = Board()
        let decoder = ControlledDecoder()
        let m = model(hub, board, decoder: { data, maxPixel in await decoder.decode(data, maxPixel: maxPixel) })
        let old = imageContent(try png(width: 40, height: 20))
        let new = imageContent(try png(width: 60, height: 30))
        var answers = [old, new]
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in try JSON.encoder.encode(answers.removeFirst()) }

        let oldFetch = Task { await m.fetch(machineID: "mac", maxPixel: 100) }
        await decoder.wait(for: 1)
        let newFetch = Task { await m.fetch(machineID: "mac", maxPixel: 100) }
        await decoder.wait(for: 2)

        let newImage = picture(width: 60, height: 30, color: .blue)
        decoder.finish(1, image: newImage)
        #expect(await newFetch.value == .fetched(.image))
        #expect(m.fetched == new && m.fetchedImage === newImage && board.content == new)

        let oldImage = picture(width: 40, height: 20, color: .green)
        decoder.finish(0, image: oldImage)
        #expect(await oldFetch.value == nil, "a superseded fetch has no result to report")
        #expect(m.fetched == new && m.fetchedImage === newImage && board.content == new)
        #expect(m.busy == nil)
    }

    @Test func clearingThePreviewDropsItsDecodedImage() async throws {
        let hub = FakeHub()
        let m = model(hub, Board())
        let content = imageContent(try png(width: 40, height: 20))
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in try JSON.encoder.encode(content) }
        #expect(await m.fetch(machineID: "mac", maxPixel: 100) == .fetched(.image))
        #expect(m.fetchedImage != nil)

        m.preview(fetched: nil, hint: false)
        #expect(m.fetched == nil && m.fetchedImage == nil)

        #expect(await m.fetch(machineID: "mac", maxPixel: 100) == .fetched(.image))
        #expect(m.fetchedImage != nil)
        hub.on("PUT", "/api/v1/machines/mac/clipboard/settings", json: #"{"supported":true,"enabled":false}"#)
        #expect(await m.setEnabled(false, machineID: "mac"))
        #expect(m.fetched == nil && m.fetchedImage == nil)
    }

    @Test func nonPictureBytesPublishNoImage() async {
        let hub = FakeHub()
        let board = Board()
        let m = model(hub, board)
        let invalid = imageContent(Data("not a picture".utf8))
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in try JSON.encoder.encode(invalid) }

        #expect(await m.fetch(machineID: "mac", maxPixel: 100) == .fetched(.image))
        #expect(m.fetched == invalid && board.content == invalid, "the existing content and outcome behavior is unchanged")
        #expect(m.fetchedImage == nil)
    }

    @Test func hubRefusalsInWords() async {
        let hub = FakeHub()
        let board = Board()
        board.content = .text("x")
        let m = model(hub, board)
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in
            throw APIError(status: 403, message: "clipboard is off", fields: ["code": "clipboard_disabled"])
        }
        #expect(await m.fetch(machineID: "mac") == .failed(.disabled))
        hub.on("GET", "/api/v1/machines/mac/clipboard") { _ in
            throw APIError(status: 409, message: "no graphical session", fields: ["code": "clipboard_unsupported"])
        }
        #expect(await m.fetch(machineID: "mac") == .failed(.unsupported("no graphical session")))
        hub.on("PUT", "/api/v1/machines/mac/clipboard") { _ in throw APIError(status: 503, message: "offline") }
        #expect(await m.sendPasteboard(machineID: "mac") == .failed(.offline))
        #expect(ClipboardOutcome.failed(.offline).message(machine: "MacBook").contains("MacBook"))
        #expect(ClipboardOutcome.failed(.unsupported("no graphical session")).message(machine: "Box").contains("no graphical session"))
    }

    @Test func switchSavesAndTellsTheList() async throws {
        let hub = FakeHub()
        hub.on("PUT", "/api/v1/machines/mac/clipboard/settings") { req in
            let body = try JSON.decoder.decode([String: Bool].self, from: #require(req.body))
            return Data(#"{"supported":true,"enabled":\#(body["enabled"] == true)}"#.utf8)
        }
        var told: [MachineClipboard] = []
        let m = model(hub, Board(), updated: { told.append($0) })
        let listed = MachineClipboard(supported: true, enabled: false)
        #expect(await m.setEnabled(true, machineID: "mac"))
        #expect(m.state(listed)?.enabled == true && told == [MachineClipboard(supported: true, enabled: true)])
        m.synchronize(MachineClipboard(supported: true, enabled: true))
        #expect(m.state(listed) == listed, "the list caught up: it is the truth again")

        hub.on("PUT", "/api/v1/machines/mac/clipboard/settings") { _ in throw APIError(status: 503, message: "offline") }
        #expect(!(await m.setEnabled(false, machineID: "mac")))
        #expect(m.outcome == .failed(.offline) && told.count == 1)
    }

    @Test func noHintWhereThereIsNoPastePrompt() async {
        let hub = FakeHub()
        hub.on("PUT", "/api/v1/machines/mac/clipboard") { _ in Data() }
        let board = Board()
        board.content = .text("x")
        let m = model(hub, board, hints: Hints().store(applies: false))
        #expect(await m.sendPasteboard(machineID: "mac") == .sent(.text))
        #expect(!m.showsPasteHint)
    }

    /// docs/operations/clipboard.md: `reason_code`, the extra refusals, and sizes checked before anything is sent.
    @Test func reasonCodesAndExtraRefusals() throws {
        let m = try JSON.decoder.decode(MachineClipboard.self, from: Data(#"{"supported":false,"enabled":true,"reason":"没有桌面会话","reason_code":"no_display"}"#.utf8))
        #expect(m.reasonCode == "no_display" && m.why == "没有桌面会话" && !m.usable)
        #expect(MachineClipboard(reasonCode: "node_outdated").why?.isEmpty == false, "a code alone still says why")
        #expect(MachineClipboard(reasonCode: "future").why == nil)

        func failure(_ status: Int, _ code: String?, _ message: String = "x") -> ClipboardFailure {
            ClipboardFailure(APIError(status: status, message: message, fields: code.map { ["code": $0] } ?? [:]))
        }
        #expect(failure(403, "read_only") == .readOnly)
        #expect(failure(403, "clipboard_disabled") == .disabled)
        #expect(failure(413, "too_large") == .tooLarge && failure(413, nil) == .tooLarge)
        #expect(failure(400, "clipboard_bad_image") == .badImage)
        #expect(failure(502, "clipboard_failed", "pasteboard busy") == .failed("pasteboard busy"))
        #expect(failure(503, "offline") == .offline)
        #expect(failure(400, "bad_request", "need text or image") == .other("need text or image"))
        #expect(ClipboardFailure.failed("pasteboard busy").sentence(machine: "MacBook").contains("pasteboard busy"))
    }

    @Test func tooLargeIsSaidBeforeSending() async {
        let hub = FakeHub()
        let board = Board()
        board.content = .text(String(repeating: "a", count: ClipboardLimits.textBytes + 1))
        let m = model(hub, board)
        #expect(await m.sendPasteboard(machineID: "mac") == .failed(.tooLarge))
        let big = ClipboardImage(mime: "image/jpeg", bytes: Data(count: ClipboardLimits.imageBytes + 3))
        #expect(await m.send(.image(big), machineID: "mac") == .failed(.tooLarge))
        #expect(hub.requests.isEmpty, "nothing uploaded")
        #expect(!ClipboardLimits.tooLarge(.text(String(repeating: "中", count: 1000))))
        #expect(!ClipboardLimits.tooLarge(.image(ClipboardImage(mime: "image/png", bytes: Data(count: ClipboardLimits.imageBytes)))))
    }

    @Test func refreshReadsTheSwitchFromTheHub() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/mac/clipboard/settings", json: #"{"supported":true,"enabled":true}"#)
        let m = model(hub, Board())
        let listed = MachineClipboard(supported: true, enabled: false)
        await m.refresh(machineID: "mac")
        #expect(m.state(listed)?.enabled == true)
        m.synchronize(MachineClipboard(supported: true, enabled: false))
        #expect(m.state(listed) == listed, "a pushed machine list wins again")
    }
}
