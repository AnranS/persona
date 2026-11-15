import Foundation
import Observation
import UIKit

/// A computer's clipboard section (#3064): its switch, Send Clipboard (the phone's to the computer), Get Clipboard (the
/// computer's onto the phone), and the one-time hint to stop iOS asking before every paste. The hub and the phone's
/// pasteboard come in through `Deps`, so the tests run it with a FakeHub and a pasteboard of their own.
@Observable final class ClipboardModel {
    typealias Decoder = @Sendable (Data, CGFloat) async -> ExtensionPicture.Decoded?

    private struct PreviewResize: Equatable {
        var content: ClipboardContent
        var maxPixel: CGFloat
    }

    struct Deps {
        var api: () -> APIClient?
        var pasteboard: PhonePasteboard
        /// The switch's new state, for the app's machine list.
        var updated: (MachineClipboard) -> Void
        var hints: ClipboardHints
    }

    enum Busy: Equatable { case sending, fetching, saving }

    private(set) var busy: Busy?
    /// The switch as last saved here (nil: as the hub's machine list says).
    private(set) var saved: MachineClipboard?
    /// What Get Clipboard put on the phone, for its preview.
    private(set) var fetched: ClipboardContent?
    /// The fetched picture, decoded once at the preview's size rather than in the view's body.
    private(set) var fetchedImage: UIImage?
    /// The last result, said under the buttons.
    private(set) var outcome: ClipboardOutcome?
    /// "Asked every time?": after the first paste the user allowed (iOS only), until dismissed.
    private(set) var showsPasteHint = false
    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private let decoder: Decoder
    @ObservationIgnored private var fetchGeneration = 0
    @ObservationIgnored private var pendingResize: PreviewResize?
    @ObservationIgnored private var resizeTask: Task<Void, Never>?
    @ObservationIgnored private var fetchedImageMaxPixel: CGFloat = 0

    init(deps: Deps, decoder: Decoder? = nil) {
        self.deps = deps
        self.decoder = decoder ?? { data, maxPixel in
            await Task.detached(priority: .userInitiated) {
                ExtensionPicture.decode(data, maxPixel: maxPixel)
            }.value
        }
    }

    /// Send Clipboard: reads the phone's clipboard (iOS may ask "Allow Paste?") and puts it on the computer.
    @discardableResult func sendPasteboard(machineID: String) async -> ClipboardOutcome {
        guard busy == nil else { return outcome ?? .nothingToSend }
        guard let content = deps.pasteboard.read() else { return finish(.nothingToSend) }
        // Read, so the user allowed the paste (or never had to): the time to say it can be always allowed.
        if deps.hints.applies && !deps.hints.dismissed() { showsPasteHint = true }
        return await send(content, machineID: machineID)
    }

    /// What the system Paste button handed over (no prompt), or a shortcut's input.
    @discardableResult func send(_ content: ClipboardContent?, machineID: String) async -> ClipboardOutcome {
        guard let content, content.kind == .text || content.kind == .image else { return finish(.nothingToSend) }
        guard busy == nil else { return outcome ?? .nothingToSend }
        busy = .sending
        defer { busy = nil }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            try await api.setClipboard(machine: machineID, content)
            return finish(.sent(content.kind))
        } catch {
            return finish(.failed(ClipboardFailure(error)))
        }
    }

    /// Get Clipboard: the computer's clipboard onto the phone's (no prompt), kept for the preview. A newer fetch may
    /// replace one still decoding; a superseded fetch returns nil and publishes nothing.
    @discardableResult func fetch(machineID: String, maxPixel: CGFloat = Theme.Size.box200 * 3) async -> ClipboardOutcome? {
        guard busy == nil || busy == .fetching else { return outcome ?? .nothingToSend }
        fetchGeneration += 1
        let generation = fetchGeneration
        busy = .fetching
        defer {
            if fetchGeneration == generation { busy = nil }
        }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            let content = try await api.clipboard(machine: machineID)
            guard fetchGeneration == generation else { return nil }
            switch content.kind {
            case .text where content.text?.isEmpty == false:
                deps.pasteboard.write(content)
                pendingResize = nil
                fetched = content
                fetchedImage = nil
                fetchedImageMaxPixel = 0
                return finish(.fetched(content.kind))
            case .image:
                guard let bytes = content.image?.bytes else { return finish(.remoteEmpty) }
                let decoded = await decoder(bytes, maxPixel)
                guard fetchGeneration == generation else { return nil }
                deps.pasteboard.write(content)
                pendingResize = nil
                fetched = content
                fetchedImage = decoded?.image
                fetchedImageMaxPixel = maxPixel
                return finish(.fetched(content.kind))
            case .concealed:
                return finish(.remoteConcealed)
            default:
                return finish(.remoteEmpty)
            }
        } catch {
            guard fetchGeneration == generation else { return nil }
            return finish(.failed(ClipboardFailure(error)))
        }
    }

    /// Whether a larger bound would sharpen the current picture; the view checks this before its resize debounce.
    func needsPreviewResize(maxPixel: CGFloat) -> Bool {
        fetched?.kind == .image && maxPixel > fetchedImageMaxPixel
    }

    /// A wider window or a new display scale needs more pixels for the same fetched picture. Keep the current preview
    /// visible while the original bytes are downsampled again off the main actor; never refetch or shrink it here. One
    /// model-owned worker outlives a cancelled view task and coalesces requests, so a window drag never overlaps decodes.
    func resizePreview(maxPixel: CGFloat) async {
        guard let content = fetched, content.kind == .image, maxPixel > fetchedImageMaxPixel,
              content.image?.bytes != nil else { return }
        if pendingResize?.content != content || (pendingResize?.maxPixel ?? 0) < maxPixel {
            pendingResize = PreviewResize(content: content, maxPixel: maxPixel)
        }
        if resizeTask == nil {
            resizeTask = Task { [weak self] in
                guard let self else { return }
                await self.drainPreviewResizes()
            }
        }
        await resizeTask?.value
    }

    private func drainPreviewResizes() async {
        while let request = pendingResize {
            guard fetched == request.content, request.maxPixel > fetchedImageMaxPixel,
                  let bytes = request.content.image?.bytes else {
                pendingResize = nil
                continue
            }
            let decoded = await decoder(bytes, request.maxPixel)
            guard pendingResize == request else { continue } // a newer size or content superseded this decode
            pendingResize = nil
            guard fetched == request.content else { continue }
            if let decoded { fetchedImage = decoded.image }
            fetchedImageMaxPixel = request.maxPixel
        }
        resizeTask = nil
    }

    /// The computer's switch; the result goes to the machine list too.
    @discardableResult func setEnabled(_ on: Bool, machineID: String) async -> Bool {
        guard busy == nil else { return false }
        busy = .saving
        defer { busy = nil }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            let state = try await api.setClipboardEnabled(machine: machineID, enabled: on)
            saved = state
            deps.updated(state)
            if !state.enabled {
                pendingResize = nil
                fetched = nil
                fetchedImage = nil
                fetchedImageMaxPixel = 0
            }
            outcome = nil
            return true
        } catch {
            outcome = .failed(ClipboardFailure(error))
            return false
        }
    }

    /// The computer's state from the hub now (`GET …/clipboard/settings`), for a page opened on an older machine list;
    /// later changes come with the pushed list. Quiet when it fails: the list's state stays.
    func refresh(machineID: String) async {
        guard busy == nil, let api = deps.api(), let state = try? await api.clipboardSettings(machine: machineID) else { return }
        if busy == nil { saved = state }
    }

    /// "Don't show again", or Open Settings: the hint does not come back.
    func dismissPasteHint() {
        showsPasteHint = false
        deps.hints.dismiss()
    }

    /// The switch as shown: saved here, else the machine list's.
    func state(_ listed: MachineClipboard?) -> MachineClipboard? { saved ?? listed }

    /// The machine list changed (a push, a refresh, or this switch passed on): it is the truth again.
    func synchronize(_ listed: MachineClipboard?) {
        if busy != .saving { saved = nil }
    }

    private func finish(_ result: ClipboardOutcome) -> ClipboardOutcome {
        outcome = result
        return result
    }

    #if DEBUG
    /// Screenshots: a state without a hub (`-showClipboardFetched text|image`, `-showClipboardHint YES`).
    func preview(fetched: ClipboardContent?, hint: Bool) {
        pendingResize = nil
        self.fetched = fetched
        fetchedImage = nil
        fetchedImageMaxPixel = 0
        if let fetched { outcome = .fetched(fetched.kind) }
        showsPasteHint = hint
    }
    #endif
}

/// The once-only paste hint's flag. Written once, read when a paste is allowed: UserDefaults is fine here.
struct ClipboardHints {
    /// Whether this platform has a paste prompt to stop (iOS: Settings › Persona › Paste from Other Apps).
    var applies: Bool
    var dismissed: () -> Bool
    var dismiss: () -> Void

    static let dismissedKey = "clipboard.pasteHintDismissed"

    static func live(_ defaults: UserDefaults = .standard) -> ClipboardHints {
        #if os(iOS)
        let applies = true
        #else
        let applies = false
        #endif
        return ClipboardHints(applies: applies, dismissed: { defaults.bool(forKey: dismissedKey) },
                              dismiss: { defaults.set(true, forKey: dismissedKey) })
    }
}

/// What Send or Get came to, in one sentence.
nonisolated enum ClipboardOutcome: Equatable, Sendable {
    case sent(ClipboardContent.Kind)
    case fetched(ClipboardContent.Kind)
    /// Nothing on the phone's clipboard, or the paste was not allowed.
    case nothingToSend
    case remoteEmpty
    case remoteConcealed
    case failed(ClipboardFailure)

    var isFailure: Bool {
        switch self {
        case .sent, .fetched: false
        default: true
        }
    }

    func message(machine name: String) -> String {
        switch self {
        case .sent(.image): String(localized: "Image sent to \(name)'s clipboard")
        case .sent: String(localized: "Sent to \(name)'s clipboard")
        case .fetched(.image): String(localized: "Copied the image from \(name) to this phone")
        case .fetched: String(localized: "Copied \(name)'s clipboard to this phone")
        case .nothingToSend: String(localized: "Nothing to send: the clipboard is empty, or pasting wasn't allowed")
        case .remoteEmpty: String(localized: "\(name)'s clipboard is empty")
        case .remoteConcealed: String(localized: "\(name)'s clipboard holds hidden content (such as a password), so it wasn't copied")
        case .failed(let f): f.sentence(machine: name)
        }
    }
}
