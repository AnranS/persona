import CoreGraphics
import Foundation
import Observation
import PersonaWire

/// The canvas transform: a scale and an offset in screen points, persisted per graph key. Automatic fits are not
/// marked as touched; once the person pans, zooms or explicitly fits, redraws keep this transform.
nonisolated struct ExtensionGraphTransform: Codable, Equatable, Sendable {
    var scale = 1.0
    var x = 0.0
    var y = 0.0

    static let minimumScale = 0.25
    static let maximumScale = 2.0

    func apply(to point: CGPoint) -> CGPoint {
        CGPoint(x: point.x * scale + x, y: point.y * scale + y)
    }

    func zoom(_ factor: Double, in viewport: CGSize) -> ExtensionGraphTransform {
        let next = min(max(scale * factor, Self.minimumScale), Self.maximumScale)
        guard next != scale else { return self }
        let center = CGPoint(x: viewport.width / 2, y: viewport.height / 2)
        let ratio = next / scale
        return ExtensionGraphTransform(scale: next,
            x: center.x - (center.x - x) * ratio,
            y: center.y - (center.y - y) * ratio)
    }

    static func fit(_ content: CGSize, in viewport: CGSize, minimum: Double = 0.25,
                    maximum: Double = 1.0) -> ExtensionGraphTransform {
        guard content.width > 0, content.height > 0, viewport.width > 0, viewport.height > 0 else {
            return ExtensionGraphTransform()
        }
        let scale = min(max(min(viewport.width / content.width, viewport.height / content.height), minimum), maximum)
        return ExtensionGraphTransform(scale: scale,
            x: (viewport.width - content.width * scale) / 2,
            y: (viewport.height - content.height * scale) / 2)
    }
}

nonisolated struct ExtensionGraphSavedState: Codable, Equatable, Sendable {
    var transform: ExtensionGraphTransform
    /// A node's explicit choice, false included: an unfolded default-folded node must stay unfolded.
    var folds: [String: Bool]
    var touched: Bool

    init(transform: ExtensionGraphTransform = ExtensionGraphTransform(), folds: [String: Bool] = [:], touched: Bool = false) {
        self.transform = transform
        self.folds = folds
        self.touched = touched
    }
}

private nonisolated struct ExtensionGraphSavedDocument: Codable, Sendable {
    var recent: [String] = []
    var pages: [String: ExtensionGraphSavedState] = [:]
}

/// One bounded, actor-serialized state-cache document for every graph. Reads and writes are off the main actor.
private actor ExtensionGraphDiskStore {
    static let shared = ExtensionGraphDiskStore()
    private static let name = "extension-graph-scenes"
    private static let limit = 64
    private var document: ExtensionGraphSavedDocument?

    func state(_ key: String) -> ExtensionGraphSavedState? {
        loadDocument().pages[key]
    }

    func save(_ key: String, _ state: ExtensionGraphSavedState) {
        var document = loadDocument()
        document.pages[key] = state
        document.recent.removeAll { $0 == key }
        document.recent.append(key)
        if document.recent.count > Self.limit {
            let gone = document.recent.prefix(document.recent.count - Self.limit)
            document.recent.removeFirst(document.recent.count - Self.limit)
            for key in gone { document.pages[key] = nil }
        }
        self.document = document
        StateCache.save(Self.name, document)
    }

    private func loadDocument() -> ExtensionGraphSavedDocument {
        if let document { return document }
        let loaded = StateCache.load(Self.name, as: ExtensionGraphSavedDocument.self) ?? ExtensionGraphSavedDocument()
        document = loaded
        return loaded
    }
}

/// The state of one graph canvas, shared by its inline drawing and full-screen cover. Graph arrangement and disk
/// restoration happen on reload; a redraw with the same key retains explicit fold choices and a touched transform.
@MainActor @Observable final class ExtensionGraphModel {
    @ObservationIgnored private(set) var graph: ExtensionGraphComponent
    private(set) var layout: ExtensionGraphLayouter.Result?
    private(set) var folded: Set<String>
    private(set) var transform = ExtensionGraphTransform()
    private(set) var touched = false
    private(set) var needsFit = true
    private(set) var layoutVersion = 0

    @ObservationIgnored let persistenceKey: String
    @ObservationIgnored private let keeps: Bool
    @ObservationIgnored private let onSave: ((ExtensionGraphSavedState) -> Void)?
    @ObservationIgnored private var foldChoices: [String: Bool]
    @ObservationIgnored private var restored: Bool
    @ObservationIgnored private var revision = 0

    /// `keeps: false` is the in-memory store used by tests and share pictures. `restored` injects their starting state.
    init(graph: ExtensionGraphComponent, restored: ExtensionGraphSavedState? = nil, keeps: Bool = true,
         onSave: ((ExtensionGraphSavedState) -> Void)? = nil) {
        self.graph = graph
        persistenceKey = graph.key ?? ""
        self.keeps = keeps
        self.onSave = onSave
        foldChoices = restored?.folds ?? [:]
        self.restored = restored != nil || !keeps || graph.key?.isEmpty != false
        var folds = Set(graph.folded ?? [])
        for (id, folded) in foldChoices {
            if folded { folds.insert(id) } else { folds.remove(id) }
        }
        folded = folds
        if let restored {
            transform = restored.transform
            touched = restored.touched
            needsFit = !restored.touched
        }
    }

    /// Recomputes only what follows from nodes, edges or folds. The task is keyed by the whole graph in the view, so a
    /// state-only redraw produces the same positions. A superseded computation never replaces a newer fold or view.
    func reload(_ next: ExtensionGraphComponent) async {
        revision += 1
        let revision = revision
        if !restored {
            restored = true
            if keeps, !persistenceKey.isEmpty, let saved = await ExtensionGraphDiskStore.shared.state(persistenceKey) {
                apply(saved)
            }
        }
        let arranged = await Self.arrange(next, folded: folded)
        guard revision == self.revision else { return }
        graph = next
        layout = arranged
        layoutVersion += 1
        if !touched { needsFit = true }
    }

    @concurrent nonisolated private static func arrange(_ graph: ExtensionGraphComponent,
                                                        folded: Set<String>) async -> ExtensionGraphLayouter.Result {
        ExtensionGraphLayouter.layout(graph, folded: folded)
    }

    func toggleFold(_ id: String) async {
        guard layout?.folds[id] != nil else { return }
        let next = !folded.contains(id)
        if next { folded.insert(id) } else { folded.remove(id) }
        foldChoices[id] = next
        if !touched { needsFit = true }
        persist()
        await reload(graph)
    }

    func updateTransform(_ next: ExtensionGraphTransform, persist: Bool = false) {
        var next = next
        next.scale = min(max(next.scale, ExtensionGraphTransform.minimumScale), ExtensionGraphTransform.maximumScale)
        guard next != transform || !touched else { return }
        transform = next
        touched = true
        needsFit = false
        if persist { self.persist() }
    }

    func zoom(_ factor: Double, in viewport: CGSize) {
        updateTransform(transform.zoom(factor, in: viewport), persist: true)
    }

    /// Applies a fit calculated by the canvas. Automatic opening fits stay untouched; the Fit button persists its result.
    func applyFit(_ next: ExtensionGraphTransform, explicit: Bool) {
        if explicit {
            updateTransform(next, persist: true)
        } else {
            transform = next
            needsFit = false
        }
    }

    /// Full screen keeps a touched canvas exactly where it was; an untouched one takes its opening fit at the new size.
    func requestOpeningFit() {
        if !touched { needsFit = true }
    }

    private func apply(_ saved: ExtensionGraphSavedState) {
        foldChoices = saved.folds
        var folds = Set(graph.folded ?? [])
        for (id, folded) in foldChoices {
            if folded { folds.insert(id) } else { folds.remove(id) }
        }
        folded = folds
        transform = saved.transform
        touched = saved.touched
        needsFit = !saved.touched
    }

    private func persist() {
        guard keeps, !persistenceKey.isEmpty else {
            onSave?(ExtensionGraphSavedState(transform: transform, folds: foldChoices, touched: touched))
            return
        }
        let saved = ExtensionGraphSavedState(transform: transform, folds: foldChoices, touched: touched)
        onSave?(saved)
        Task { await ExtensionGraphDiskStore.shared.save(persistenceKey, saved) }
    }
}
