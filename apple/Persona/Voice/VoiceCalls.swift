import Foundation
import Observation
#if os(iOS) || os(visionOS)
import UIKit
#endif

/// The app's voice call: at most one at a time, whichever page or surface started it (docs/assistant/voice.md 6.3). ChatView only
/// asks `isBound(to:)`; the bar and the thread's tail read `current` themselves.
///
/// Rules (docs/assistant/voice.md 6.5): a new call ends the one before; the app going to the background ends a call started from
/// the phone, never one started from the car or a system call to Persona (#9290, `PersonaCalls` runs it); leaving the
/// page ends the phone's call bound to it. Starting a recording or dictation, a phone call, three failed reconnects or
/// ten idle minutes end it from inside (`VoiceConversation`; a system call holds through a phone call instead).
@MainActor @Observable final class VoiceCalls {
    static let shared = VoiceCalls(watchesApp: true)

    /// The call, live or ended. An ended one stays (「语音已结束 · 继续」) until its page goes, unless the user ended it.
    private(set) var current: VoiceConversation?

    @ObservationIgnored private var observers: [NSObjectProtocol] = []

    /// `watchesApp`: end the phone's call when the app goes to the background (the shared one; the tests drive
    /// `appDidEnterBackground` themselves).
    init(watchesApp: Bool = false) {
        #if os(iOS) || os(visionOS)
        guard watchesApp else { return }
        observers.append(NotificationCenter.default.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil,
                                                                queue: .main) { @Sendable [weak self] _ in
            MainActor.assumeIsolated { self?.appDidEnterBackground() }
        })
        #endif
    }

    /// Starts a call bound to `key`. The same conversation's call from the same surface is left as it is (or picked up
    /// again when it ended); another one is ended first, a call from another surface too (a voice chat on the main
    /// conversation while a call to Persona is on it is a call of its own). Returns once the call sent its `start`, or ended.
    func start(_ key: SessionKey, extensionID: String, surface: VoiceSurface, recent: [VoiceLine] = [], deps: VoiceDeps) async {
        if let call = current {
            // A system call that ended has no system call behind it any more: it is never picked up again.
            if call.key == key, call.extensionID == extensionID, call.surface == surface, surface != .call {
                if call.phase.isEnded { await call.restart() }
                return
            }
            call.end(.replaced)
            current = nil
        }
        let call = VoiceConversation(key: key, extensionID: extensionID, surface: surface, recent: recent, deps: deps)
        current = call
        await call.begin()
    }

    /// A call made elsewhere becomes the app's call and begins (a system call: `PersonaCalls` holds it for the system's
    /// audio before it begins). The one before ends.
    func begin(_ call: VoiceConversation) async {
        if let before = current, before !== call {
            before.end(.replaced)
        }
        current = call
        await call.begin()
    }

    /// A system call that ended goes away at once: no Continue (there is no system call to pick up again).
    func release(_ call: VoiceConversation) {
        if current === call { current = nil }
    }

    /// Continue on the ended call bound to `key` (not a system call's).
    func resume(_ key: SessionKey) async {
        guard let call = current, call.key == key, call.phase.isEnded, call.surface != .call else { return }
        await call.restart()
    }

    /// Ends the call started from `surface`; a call another surface started goes on (CarPlay only stops its own).
    /// Stop (`.user`) ends whichever it is: the user tapped it on the call itself.
    func end(_ reason: VoiceEnd, from surface: VoiceSurface) {
        guard let call = current, reason == .user || call.surface == surface else { return }
        call.end(reason)
        if reason.byUser { current = nil }
    }

    /// Whether the call (live or ended) is bound to this conversation: ChatView reads only this.
    func isBound(to key: SessionKey) -> Bool { current?.key == key }

    /// The page bound to `key` went away or was hidden: its call ends, and its Continue row goes with it.
    func leave(_ key: SessionKey, from surface: VoiceSurface = .phone) {
        guard let call = current, call.key == key, call.surface == surface else { return }
        call.end(.left)
        current = nil
    }

    /// In the background a call would keep the microphone open and the upstream billing: the phone's ends. The car's
    /// goes on.
    func appDidEnterBackground() {
        end(.background, from: .phone)
    }
}
