import SwiftUI

/// Where the visible replies' models change (#5055), projected after folded rounds are resolved and before work opens.
/// Only loaded lines participate: prepending an older page rebuilds the first newer exchange with its new predecessor.
struct ModelChanges {
    private struct Reply {
        let id: String
        let name: String
        let canonical: String

        static func normalized(_ raw: String?) -> String? {
            guard let raw else { return nil }
            let id = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            return id.isEmpty || id == "<synthetic>" ? nil : id
        }

        static func known(_ raw: String?) -> Bool { normalized(raw) != nil }

        init?(_ raw: String?) {
            guard let id = Self.normalized(raw) else { return nil }
            self.id = id
            name = replyModelName(id)
            canonical = bareModel(id).lowercased()
        }

        func equals(_ other: Self) -> Bool { canonical == other.canonical || name == other.name }
    }

    private struct Mark {
        let after: Int
        let from: Reply
        let to: Reply
    }

    static func mark(_ lines: [ChatLine]) -> [ChatLine] {
        var previous: Reply?
        var marks: [Mark] = []
        var start = 0
        var user: Int?

        func finish(_ end: Int) {
            defer { start = end }
            guard end > start else { return }
            var latest: Item?
            for line in lines[start..<end] {
                // Quiet supervision/background rounds and recaps are not visible replies. Everything in a work line,
                // including a fallback's later tool call, counts beside the final answer.
                switch line.content {
                case .supervision, .recaps, .row(.recap): continue
                default: break
                }
                for item in ChatLines.lineItems(line) where item.role == "assistant" && Reply.known(item.model) {
                    if let current = latest {
                        if (item.seq, item.part) >= (current.seq, current.part) { latest = item }
                    } else {
                        latest = item
                    }
                }
            }
            guard let latest, let current = Reply(latest.model) else { return }
            defer { previous = current }
            guard let previous, !previous.equals(current), let user else { return }
            marks.append(Mark(after: user, from: previous, to: current))
        }

        for index in 0...lines.count {
            if index < lines.count, case .row(.user) = lines[index].content {
                finish(index)
                user = index
            }
        }
        finish(lines.count)
        guard !marks.isEmpty else { return lines }

        var out: [ChatLine] = []
        out.reserveCapacity(lines.count + marks.count)
        var next = marks.makeIterator()
        var mark = next.next()
        for (index, line) in lines.enumerated() {
            out.append(line)
            if mark?.after == index, let change = mark {
                out.append(.modelChange(id: "mc-" + line.id, from: change.from.id, to: change.to.id))
                mark = next.next()
            }
        }
        return out
    }
}

/// The web's model-change row in iOS tokens: two hairlines and a non-interactive, page-colored capsule.
struct ModelChangeDivider: View {
    let names: (from: String, to: String)
    let rawFrom: String
    let rawTo: String

    private var rule: some View {
        let beautiful = AppStyle.current.usesBeautifulUI
        return Rectangle().fill(beautiful ? Theme.cardStroke : Color(uiColor: .separator))
            .frame(height: beautiful ? Theme.Size.line : Theme.Size.hairline)
            .frame(minWidth: Theme.Space.l, maxWidth: .infinity)
    }

    @ViewBuilder private func chip(wrapped: Bool) -> some View {
        HStack(spacing: Theme.Space.sm) {
            ThemedIcon(Theme.Icon.switchAgents)
                .scaledFrame(width: Theme.Size.iconXS, height: Theme.Size.iconXS)
            if wrapped {
                Text(verbatim: names.from + " → " + names.to)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Text(verbatim: names.from + " → " + names.to).lineLimit(1).fixedSize()
            }
        }
        .font(Theme.Font.label)
        .foregroundStyle(.secondary)
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.xs)
        .background(Theme.pageBackground.opacity(Theme.Fade.dividerBackground), in: .capsule)
        .overlay(Capsule().strokeBorder(Theme.cardStroke, lineWidth: Theme.Size.hairline))
    }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: Theme.Space.m) {
                rule
                chip(wrapped: false)
                rule
            }
            chip(wrapped: true)
        }
        .padding(.vertical, Theme.Space.s)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String(localized: "Model changed from \(names.from) to \(names.to)"))
        .accessibilityIdentifier("chat.modelChange")
        .help(rawFrom + " → " + rawTo)
    }
}
