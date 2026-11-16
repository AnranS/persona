#if os(iOS)
import Foundation

// One system call's rules (#9290), for both kinds: the owner calling Persona and Persona's urgent call. Pure: no
// LiveCommunicationKit, no audio. `PersonaCalls` feeds it what the system, the engine and the page did, and carries out
// what it answers (`CallEffect`), so each rule is a unit test (`CallMachineTests`) and not a device session.

/// Who the call is with and what it is about.
nonisolated enum CallKind: Equatable, Hashable, Sendable {
    /// The owner calls Persona: the main conversation of the group's hub (empty: the hub selected when it started).
    case owner(group: String)
    /// Persona's urgent call (`UrgentCallCenter`), by the manager's call id.
    case urgent(callID: String)

    var isUrgent: Bool { if case .urgent = self { true } else { false } }
}

nonisolated enum CallDirection: Equatable, Sendable {
    case outgoing, incoming
}

/// Why a call is on hold. More than one can hold it at once; it goes on only when none is left.
nonisolated enum CallHoldReason: Hashable, Sendable {
    /// The system paused it (Hold & Accept, a long press on Mute): `PauseConversationAction`.
    case system
    /// The audio session was interrupted outside a pause (a phone call or another app's call reaches the app only so:
    /// LiveCommunicationKit has no call observer).
    case interruption
}

/// What the system hears when a call ends (LiveCommunicationKit's `Conversation.EndedReason`).
nonisolated enum CallEndedReport: Equatable, Sendable {
    case failed, remoteEnded, unanswered, joinedElsewhere, declinedElsewhere
}

/// How a call ended, as the page says it.
nonisolated enum CallEndReason: Equatable, Sendable {
    /// Hung up: on the call page, in the system's call UI, with AirPods.
    case hungUp
    /// The urgent call was declined while it rang.
    case declined
    /// Nobody answered the urgent call in time.
    case unanswered
    /// The urgent call was answered or declined on another device, or the manager settled it.
    case answeredElsewhere
    case declinedElsewhere
    /// Persona's side ended it: the voice ended by itself (ten quiet minutes, a recording took the microphone, another
    /// call), the urgent conversation was over, or the system ended it.
    case remoteEnded
    /// It could not go on: no audio, the voice failed, the system refused it.
    case failed

    /// What the system hears (the endings the system itself made are not reported back).
    var report: CallEndedReport {
        switch self {
        case .unanswered: .unanswered
        case .answeredElsewhere: .joinedElsewhere
        case .declinedElsewhere: .declinedElsewhere
        case .failed: .failed
        case .hungUp, .declined, .remoteEnded: .remoteEnded
        }
    }
}

nonisolated enum CallPhase: Equatable, Sendable {
    /// Outgoing: Start asked of the system ("Calling…").
    case dialing
    /// Incoming: ringing.
    case ringing
    /// Started or answered: waiting for the audio and, calling Persona, the voice's first `ready`.
    case connecting
    case connected(since: Date)
    case ended(CallEndReason)

    var isEnded: Bool { if case .ended = self { true } else { false } }
    var isLive: Bool { !isEnded }
    var connectedSince: Date? { if case .connected(let since) = self { since } else { nil } }
}

/// A report to the system about a call.
nonisolated enum CallReport: Equatable, Sendable {
    case startedConnecting(Date)
    case connected(Date)
    /// `conversationUpdated(capabilities: [.pausing])`: the system may hold it (a long press on Mute, Hold & Accept).
    case pausable
    case ended(Date, CallEndedReport)
}

/// A wait the machine asks for: `PersonaCalls` feeds `.timer` back when it is up.
nonisolated enum CallTimer: Hashable, Sendable {
    /// No `didActivate` this long after the call started or was answered: the audio never came.
    case activation
    /// After an interruption ended, no `didActivate` this long: ask the system to resume the call.
    case recoveryUnpause
    /// The resume asked for and the session still inactive: activate it once ourselves.
    case recoveryActivate
    /// Still no audio: stay on hold and offer Resume.
    case recoveryGiveUp

    var delay: Duration {
        switch self {
        case .activation: .seconds(5)
        case .recoveryUnpause: .seconds(1)
        case .recoveryActivate: .milliseconds(500)
        case .recoveryGiveUp: .seconds(5)
        }
    }
}

/// What happened to a call.
nonisolated enum CallEvent: Equatable, Sendable {
    // The system's actions: each is fulfilled or failed (`CallEffect.fulfil` / `.fail`).
    case startAction
    case joinAction
    case endAction
    case muteAction(Bool)
    case pauseAction(Bool)
    case otherAction
    /// An action the system gave up waiting for: the system ends the call itself.
    case actionTimedOut
    // The system's other callbacks.
    /// The conversation is in the manager's `conversations`: reports made before it is are dropped by the system.
    case known
    case audioActivated
    case audioDeactivated
    /// The conversation left without an End action (the system ended it).
    case systemEnded
    /// The manager reset: every call is gone.
    case reset
    // The app.
    case muteTapped(Bool)
    /// The system refused the page's mute (`perform` threw): it applies here all the same.
    case muteRefused(Bool)
    case hangUpTapped
    /// The system refused the hang-up: it ends here and the system hears it.
    case hangUpRefused
    case resumeTapped
    /// The audio session activated by the app itself (`CallEffect.activateSession`), or that failed.
    case sessionActivated
    case sessionActivationFailed
    /// An urgent call answered without the system (its notification tapped and the system refused Join).
    case answeredHere
    /// The urgent call stopped ringing for good (missed, settled elsewhere): the system hears why.
    case settled(CallEndReason)
    // The engine.
    /// Calling Persona: the voice's first `ready`.
    case voiceReady
    /// The engine's audio runs (voice processing on, the microphone open).
    case engineRunning
    /// The voice or the urgent conversation ended by itself.
    case engineEnded(failed: Bool)
    case interruptionBegan
    case interruptionEnded
    case timer(CallTimer)
}

/// What `PersonaCalls` does for the machine, in order.
nonisolated enum CallEffect: Equatable, Sendable {
    /// The action being handled.
    case fulfil
    case fail
    case report(CallReport)
    /// The engine begins: calling Persona, the voice starts; an urgent call is answered.
    case begin
    case audioOn
    case audioOff
    case mute(Bool)
    case hold(Bool)
    /// Through the system (`MuteConversationAction`): the page's mute, or the owner's mute asserted again.
    case performMute(Bool)
    /// Through the system: `PauseConversationAction(isPaused: false)`.
    case performUnpause
    case performEnd
    /// `AVAudioSession.setActive(true)`, once: the system did not activate it.
    case activateSession
    case endEngine
    case schedule(CallTimer)
    case log(String)
}

nonisolated struct CallMachine: Equatable, Sendable {
    let id: UUID
    let kind: CallKind
    let direction: CallDirection
    private(set) var phase: CallPhase
    private(set) var muted = false
    private(set) var holds: Set<CallHoldReason> = []
    /// The system activated the call's audio session (and has not taken it away since).
    private(set) var audioActive = false
    /// The audio was active at least once: the activation watchdog is over.
    private(set) var everActive = false
    /// The conversation is in the system's list.
    private(set) var known = false
    /// Reports kept until the system knows the conversation.
    private(set) var pending: [CallReport] = []
    /// What the engine was last told about hold.
    private(set) var engineHeld = false
    /// The page's mute on its way through the system: when it comes back as an action it is the owner's.
    private(set) var requestedMute: Bool?
    /// Until then a system mute is the engine's own (enabling voice processing surfaces as one on iOS 17+), not the
    /// owner's: ignored and the owner's mute asserted again, once.
    private(set) var startingUntil: Date?
    private(set) var reasserted = false
    /// The app activated the session itself once (the activation watchdog, or the recovery after an interruption).
    private(set) var activationTried = false
    /// An interruption ended and the audio has not come back yet.
    private(set) var recovering = false
    /// The audio did not come back after an interruption: the page offers Resume.
    private(set) var resumeOffered = false

    /// How long after the engine runs a system mute is still the engine's.
    static let engineSettle: TimeInterval = 0.5

    init(id: UUID, kind: CallKind, direction: CallDirection) {
        self.id = id
        self.kind = kind
        self.direction = direction
        phase = direction == .outgoing ? .dialing : .ringing
    }

    var held: Bool { !holds.isEmpty }
    /// What the page says it is on hold for: the system's pause first.
    var holdReason: CallHoldReason? { holds.contains(.system) ? .system : holds.contains(.interruption) ? .interruption : nil }

    mutating func handle(_ event: CallEvent, at now: Date) -> [CallEffect] {
        var out: [CallEffect] = []
        func end(_ reason: CallEndReason, report reported: Bool, engine: Bool = true) -> [CallEffect] {
            finish(reason, at: now, report: reported, engine: engine)
        }
        switch event {
        case .startAction:
            guard phase == .dialing else { return [phase.isEnded ? .fail : .fulfil] }
            out.append(.fulfil)
            phase = .connecting
            out += report(.startedConnecting(now))
            out += report(.pausable)
            out.append(.begin)
            if !everActive { out.append(.schedule(.activation)) }

        case .joinAction:
            guard phase == .ringing else { return [phase.isEnded ? .fail : .fulfil] }
            // The engine begins first: answering ends an in-app voice chat before the system activates the session.
            out.append(.begin)
            out.append(.fulfil)
            phase = .connected(since: now)
            if !everActive { out.append(.schedule(.activation)) }

        case .answeredHere:
            guard phase == .ringing else { return [] }
            out.append(.begin)
            phase = .connected(since: now)
            out += activated()

        case .endAction:
            out.append(.fulfil)
            guard phase.isLive else { return out }
            out += end(phase == .ringing ? .declined : .hungUp, report: false)

        case .muteAction(let on):
            out.append(.fulfil)
            guard phase.isLive else { return out }
            if on == muted {
                // The system's echo of a mute already applied (8–17 ms after the app's own), or an unmute after a muted
                // call ended: nothing changes.
                if requestedMute == on { requestedMute = nil }
            } else if requestedMute == on {
                requestedMute = nil
                out += setMuted(on)
            } else if let until = startingUntil, now < until {
                out.append(.log("a system mute while the engine started, ignored"))
                if !reasserted {
                    reasserted = true
                    out.append(.performMute(muted))
                }
            } else {
                // The system's own: its call UI, AirPods.
                out += setMuted(on)
            }

        case .muteTapped(let on):
            guard phase.isLive, on != muted || requestedMute != nil else { return [] }
            requestedMute = on
            out.append(.performMute(on))

        case .muteRefused(let on):
            guard phase.isLive, requestedMute == on else { return [] }
            requestedMute = nil
            if on != muted { out += setMuted(on) }

        case .pauseAction(let paused):
            // At once, whatever else: a pause failed, or left about 5 s, makes the system end the call.
            out.append(.fulfil)
            guard phase.isLive else { return out }
            if paused {
                holds.insert(.system)
            } else {
                holds.remove(.system)
                resumeOffered = false
            }
            out += followHold()

        case .otherAction:
            return [.fail]

        case .actionTimedOut:
            guard phase == .dialing else { return [.log("the system gave up on an action")] }
            out.append(.log("the system gave up on the start"))
            out += end(.failed, report: true)

        case .known:
            known = true
            out += pending.map(CallEffect.report)
            pending = []

        case .audioActivated, .sessionActivated:
            guard phase.isLive else { return [] }
            if event == .sessionActivated { out.append(.log("the call's audio session activated by the app")) }
            let on = !audioActive
            out += activated()
            // A system mute is the engine's own from now until half a second after it runs (`engineRunning`). Only when
            // the audio comes on: a second word of the same activation (the watchdog's own and the system's late one)
            // starts no engine, so nothing would close the window again.
            if on { startingUntil = .distantFuture }

        case .sessionActivationFailed:
            guard phase.isLive, !audioActive else { return [] }
            guard everActive else {
                // The watchdog: the system never activated the call's audio and it would not activate either.
                out.append(.log("no audio: the system did not activate the call's audio session and it would not activate"))
                return out + end(.failed, report: true)
            }
            out.append(.log("the call's audio session would not activate after the interruption"))

        case .audioDeactivated:
            guard audioActive else { return [] }
            audioActive = false
            startingUntil = nil
            if phase.isLive { out.append(.audioOff) }

        case .engineRunning:
            guard phase.isLive else { return [] }
            startingUntil = now.addingTimeInterval(Self.engineSettle)

        case .voiceReady:
            guard phase == .connecting else { return [] }
            phase = .connected(since: now)
            out += report(.connected(now))

        case .engineEnded(let failed):
            guard phase.isLive else { return [] }
            out += end(failed ? .failed : .remoteEnded, report: true, engine: false)

        case .hangUpTapped:
            guard phase.isLive else { return [] }
            out.append(.performEnd)

        case .hangUpRefused:
            guard phase.isLive else { return [] }
            out += end(phase == .ringing ? .declined : .hungUp, report: true)

        case .systemEnded:
            guard phase.isLive else { return [] }
            out.append(.log("the system ended the call without an End action"))
            out += end(.remoteEnded, report: false)

        case .reset:
            guard phase.isLive else { return [] }
            out += end(.failed, report: false)

        case .settled(let reason):
            guard phase.isLive else { return [] }
            out += end(reason, report: true, engine: false)

        case .resumeTapped:
            guard phase.isLive, held else { return [] }
            resumeOffered = false
            out.append(.performUnpause)
            if holds.contains(.interruption), !audioActive {
                recovering = true
                activationTried = false
                out.append(.schedule(.recoveryActivate))
                out.append(.schedule(.recoveryGiveUp))
            }

        case .interruptionBegan:
            // Only once the call has its audio: a ring or a dial has no session of the app's to interrupt.
            guard phase == .connecting || phase.connectedSince != nil else { return [] }
            // The session is not the call's any more, whether or not the system also says so (`didDeactivate` may never
            // come): the engine goes (`PhoneVoiceAudio` took its own down already), the recovery's waits see no audio,
            // and the next activation starts it again.
            if audioActive {
                audioActive = false
                startingUntil = nil
                out.append(.audioOff)
            }
            holds.insert(.interruption)
            recovering = false
            resumeOffered = false
            out += followHold()

        case .interruptionEnded:
            guard phase.isLive, holds.contains(.interruption) else { return [] }
            // Still paused by the system: its resume brings the audio back.
            guard !holds.contains(.system) else { return [] }
            recovering = true
            activationTried = false
            out.append(.schedule(.recoveryUnpause))
            out.append(.schedule(.recoveryGiveUp))

        case .timer(let timer):
            out += fire(timer)
        }
        return out
    }

    // MARK: Rules

    private mutating func fire(_ timer: CallTimer) -> [CallEffect] {
        guard phase.isLive else { return [] }
        switch timer {
        case .activation:
            guard !everActive, !activationTried else { return [] }
            activationTried = true
            return [.log("no didActivate 5 s after the call started: activating the audio session once"), .activateSession]
        case .recoveryUnpause:
            guard recovering, !audioActive, !holds.contains(.system) else { return [] }
            return [.log("no didActivate 1 s after the interruption ended: asking the system to resume"), .performUnpause,
                    .schedule(.recoveryActivate)]
        case .recoveryActivate:
            guard recovering, !audioActive, !activationTried else { return [] }
            activationTried = true
            return [.activateSession]
        case .recoveryGiveUp:
            guard recovering, !audioActive, holds.contains(.interruption) else { return [] }
            recovering = false
            resumeOffered = true
            return [.log("the audio did not come back after the interruption: on hold, Resume offered")]
        }
    }

    /// The audio session is the call's (the system's `didActivate`, or the app's own activation).
    private mutating func activated() -> [CallEffect] {
        var out: [CallEffect] = []
        let first = !audioActive
        audioActive = true
        everActive = true
        // The system gave the session back: whatever interrupted it is over.
        holds.remove(.interruption)
        recovering = false
        resumeOffered = false
        if first { out.append(.audioOn) }
        out += followHold()
        return out
    }

    /// Tells the engine whether it is on hold: held as soon as anything holds it; going on only once nothing does and the
    /// audio is the call's again.
    private mutating func followHold() -> [CallEffect] {
        let wanted = held || (engineHeld && !audioActive)
        guard wanted != engineHeld else { return [] }
        engineHeld = wanted
        return [.hold(wanted)]
    }

    private mutating func setMuted(_ on: Bool) -> [CallEffect] {
        muted = on
        return [.mute(on)]
    }

    /// A report now if the system knows the conversation, else when it does.
    private mutating func report(_ r: CallReport) -> [CallEffect] {
        if known { return [.report(r)] }
        pending.append(r)
        return []
    }

    /// Ends the call once: the engine (unless it is the one that ended), and the system hears it unless the system ended it.
    private mutating func finish(_ reason: CallEndReason, at now: Date, report reported: Bool, engine: Bool) -> [CallEffect] {
        var out: [CallEffect] = []
        phase = .ended(reason)
        recovering = false
        resumeOffered = false
        requestedMute = nil
        // Only the ending is still worth saying to a system that does not know the conversation yet.
        pending = []
        if reported { out += report(.ended(now, reason.report)) }
        if engine { out.append(.endEngine) }
        return out
    }
}
#endif
