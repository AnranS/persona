import AppIntents
import Foundation

/// "Talk to Persona": the hands-free voice conversation on the CarPlay screen when Persona's voice screen is in the car
/// (`VoiceConversation(surface: .carPlay)` via `VoiceCalls`, in the background as before). With no car connected it is a
/// call to Persona on the phone (#9290), as Call Persona (`CallPersonaIntent`) places it: the app comes to the front for
/// it. CarPlay connected but Persona not on the car's screen still says to open it there: a call that needs the phone
/// unlocked is the wrong answer while driving.
struct TalkToPersonaIntent: AppIntent {
    nonisolated static let title: LocalizedStringResource = "Talk to Persona"
    // App Intent metadata says "this device", never "iPhone": App Store Connect rejects it (90626, #6471).
    nonisolated static let description = IntentDescription(
        "Talk with Persona hands-free: on CarPlay, a continuous voice chat on the car's screen; elsewhere, a call to Persona on this device. You talk, it answers, and you can cut in anytime.",
        categoryName: "Chat",
        searchKeywords: ["Persona", "voice", "talk", "chat", "CarPlay", "call"]
    )
    #if os(iOS)
    // In the background for CarPlay (the phone may stay locked in the car); in front only for the call on the phone.
    nonisolated static var supportedModes: IntentModes { [.background, .foreground(.dynamic)] }
    #else
    nonisolated static var supportedModes: IntentModes { .background }
    #endif

    nonisolated init() {}

    #if os(iOS)
    nonisolated func perform() async throws -> some IntentResult & ProvidesDialog {
        // Connected is not enough: the voice screen must be the one in the car while the microphone is on.
        let talk = await MainActor.run { CarPlayVoicePresence.talk }
        if talk.callsOnThePhone {
            // The system takes a call's Start only from an app in front; the call's page shows there.
            try await continueInForeground(alwaysConfirm: false)
            await MainActor.run { CallPersonaIntent.place() }
            return .result(dialog: IntentSupport.dialog(CallPersonaIntent.calling))
        }
        guard talk == .start else {
            return .result(dialog: IntentSupport.dialog(talk.sentence))
        }
        // Give Siri a moment to finish its own sentence before the microphone opens; the user may leave meanwhile.
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(1.5))
            guard CarPlayVoicePresence.talk == .start else { return }
            _ = await CarPlayVoiceSession.start()
        }
        return .result(dialog: IntentSupport.dialog(talk.sentence))
    }
    #else
    // macOS has no CarPlay: the same sentence as the unconnected case.
    nonisolated func perform() async throws -> some IntentResult & ProvidesDialog {
        .result(dialog: IntentSupport.dialog(CarPlayTalk.notConnected.sentence))
    }
    #endif
}
