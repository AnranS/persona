import Foundation
import Testing
@testable import Persona

/// The system's side faked (#9290): what `PersonaCalls` asked of it, and the actions and audio callbacks a test hands
/// back, as LiveCommunicationKit's delegate would.
@MainActor final class FakeCallSystem: CallSystem {
    weak var handler: (any CallSystemHandler)?
    private(set) var starts: [UUID] = []
    private(set) var rings: [(id: UUID, name: String)] = []
    private(set) var requests: [CallRequest] = []
    private(set) var reports: [CallReport] = []
    private(set) var fulfilled: [CallSystemAction] = []
    private(set) var failed: [CallSystemAction] = []
    private(set) var activations = 0
    var known: Set<UUID> = []
    /// Started or reported calls are in its list at once; false: only once a test says so.
    var knowsAtOnce = true
    /// Thrown by the next Starts, in order.
    var startErrors: [any Error] = []
    var requestError: (any Error)?
    var activates = true

    func start(_ id: UUID) async throws {
        starts.append(id)
        if !startErrors.isEmpty { throw startErrors.removeFirst() }
        if knowsAtOnce { known.insert(id) }
    }

    func reportIncoming(_ id: UUID, name: String) async throws {
        rings.append((id, name))
        known.insert(id)
    }

    func perform(_ request: CallRequest, for id: UUID) async throws {
        requests.append(request)
        if let requestError { throw requestError }
    }

    func report(_ report: CallReport, for id: UUID) { reports.append(report) }

    func knows(_ id: UUID) -> Bool { known.contains(id) }

    func activateAudio() async -> Bool {
        activations += 1
        return activates
    }

    /// The system asks the app to carry out `action`.
    func act(_ action: CallSystemAction, _ id: UUID) {
        handler?.system(perform: action, for: id, fulfil: { self.fulfilled.append(action) }, fail: { self.failed.append(action) })
    }

    func activate() { handler?.systemActivatedAudio() }
    func deactivate() { handler?.systemDeactivatedAudio() }

    var ended: [CallEndedReport] { reports.compactMap { if case .ended(_, let why) = $0 { why } else { nil } } }
}

/// A `PersonaCalls` on the fake system, the voice on `VoiceRig`'s fakes, the clock moved by hand.
@MainActor final class CallRig {
    let system = FakeCallSystem()
    let voice = VoiceRig()
    let voiceCalls = VoiceCalls()
    /// The groups whose call to Persona ended (`PersonaCalls.callLeft`: their tunnels may go).
    private(set) var left: [String] = []
    private(set) lazy var calls: PersonaCalls = {
        let calls = PersonaCalls(system: { [system] in system }, clock: voice.clock, voiceCalls: voiceCalls,
                                 ownerAudio: { [voice] in voice.audio }, ownerDeps: { [voice] _, audio, events in
            var deps = voice.deps
            deps.audio = { audio }
            deps.callEvents = events
            return deps
        })
        calls.callLeft = { [weak self] in self?.left.append($0) }
        return calls
    }()

    /// A call to Persona the system was asked to start (it has not handed Start back yet).
    func dial() async -> PersonaCall {
        let call = calls.add(kind: .owner(group: voice.key.group), direction: .outgoing)
        attachEngine(call)
        await calls.place(call, origin: .app)
        return call
    }

    /// The call's voice engine, as `callPersona` attaches it once the hub, the extension and the microphone said yes.
    func attachEngine(_ call: PersonaCall) {
        let deps = calls.ownerDeps
        let key = voice.key
        let engine = OwnerCallEngine(key: key, extensionID: "voice", recent: [], audio: voice.audio, calls: voiceCalls,
                                     deps: { audio, events in deps(key.group, audio, events) })
        calls.attach(engine, to: call)
    }

    /// Started by the system, its audio activated and the voice ready.
    func connected() async -> (PersonaCall, FakeVoiceSocket) {
        let call = await dial()
        system.act(.start, call.id)
        await voice.waitUntil { voice.socket != nil }
        system.activate()
        let socket = voice.socket!
        socket.ready(voice.key)
        await voice.waitUntil { call.connectedSince != nil }
        return (call, socket)
    }

    func settle() async { for _ in 0..<20 { await Task.yield() } }
}

/// The app's one call manager (#9290): the system's actions reach the call by its id, the machine says what to do and
/// `PersonaCalls` does it, for a call to Persona on a real `VoiceConversation(.call)` over fakes, and for urgent calls.
@MainActor
struct PersonaCallsTests {
    @Test func aCallToPersonaStartsTheVoiceOnStartAndItsAudioOnlyOnActivation() async throws {
        let rig = CallRig()
        let call = await rig.dial()
        #expect(rig.system.starts == [call.id] && call.phase == .dialing)
        #expect(rig.calls.ownerCall === call)
        rig.system.act(.start, call.id)
        #expect(rig.system.fulfilled == [.start])
        #expect(call.phase == .connecting)
        #expect(rig.system.reports.prefix(2).map { if case .startedConnecting = $0 { "connecting" } else { "\($0)" } } == ["connecting", "pausable"])
        await rig.voice.waitUntil { rig.voice.socket != nil }
        let socket = try #require(rig.voice.socket)
        #expect(socket.start?["surface"] as? String == "call")
        #expect(rig.voice.audio.running && !rig.voice.audio.active, "the microphone waits for the system's activation")
        #expect(rig.voiceCalls.current === call.voice)

        rig.system.activate()
        #expect(rig.voice.audio.activations == 1)
        socket.ready(rig.voice.key)
        #expect(call.connectedSince != nil)
        #expect(rig.system.reports.contains { if case .connected = $0 { true } else { false } })
    }

    @Test func reportsWaitUntilTheSystemHasTheConversation() async {
        let rig = CallRig()
        rig.system.knowsAtOnce = false
        let call = await rig.dial()
        rig.system.act(.start, call.id)
        #expect(rig.system.reports.isEmpty, "dropped by a system that does not have it yet")
        rig.calls.system(changed: call.id, state: .idle)
        #expect(rig.system.reports == [.startedConnecting(rig.voice.clock.now), .pausable])
    }

    @Test func theBackgroundAnotherPageOrTheLockNeverEndTheCall() async {
        let rig = CallRig()
        let (call, socket) = await rig.connected()
        rig.voiceCalls.appDidEnterBackground()
        rig.voiceCalls.leave(rig.voice.key)
        rig.calls.minimise()
        #expect(call.phase.isLive && call.voice?.phase.isEnded == false && !socket.closed)
        #expect(rig.system.ended.isEmpty)
    }

    @Test func hangingUpEndsBothSidesOnce() async {
        let rig = CallRig()
        let (call, socket) = await rig.connected()
        rig.calls.hangUp()
        await rig.settle()
        #expect(rig.system.requests == [.end])
        rig.system.act(.end, call.id)
        #expect(rig.system.fulfilled.last == .end)
        #expect(call.phase == .ended(.hungUp) && call.note == nil)
        #expect(call.voice?.phase == .ended(.user) && socket.closed)
        #expect(rig.system.ended.isEmpty, "the system ended it: nothing to report back")
        #expect(rig.voiceCalls.current == nil, "no Continue after a system call")
    }

    @Test func theVoiceEndingByItselfEndsTheSystemsCall() async {
        let lost = CallRig()
        let (call, _) = await lost.connected()
        call.voice?.end(.lost)
        #expect(call.phase == .ended(.failed))
        #expect(lost.system.ended == [.failed])
        #expect(call.note == VoiceEndNote.make(.lost, extensionID: "voice")?.text)
        // The ended voice stays for the main conversation's 「通话已结束 · …」 (its Continue calls Persona again), and is
        // never picked up again itself; leaving the page lets it go.
        #expect(lost.voiceCalls.current === call.voice)
        let picked = await lost.voiceCalls.resume(lost.voice.key)
        #expect(picked == nil && call.voice?.phase == .ended(.lost))
        #expect(lost.voice.sockets.count == 1, "no second socket for an ended system call")
        lost.voiceCalls.leave(lost.voice.key)
        #expect(lost.voiceCalls.current == nil)

        let idle = CallRig()
        let (quiet, _) = await idle.connected()
        quiet.voice?.end(.idle)
        #expect(idle.system.ended == [.remoteEnded])
    }

    @Test func anotherChatsVoiceEndsTheCallAndItsSystemCall() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        let other = SessionKey(group: rig.voice.key.group, machineID: "m2", sessionID: "s2")
        await rig.voiceCalls.start(other, extensionID: "voice", surface: .phone, deps: rig.voice.deps)
        #expect(call.phase == .ended(.remoteEnded))
        #expect(rig.system.ended == [.remoteEnded])
    }

    @Test func aPauseHoldsTheVoiceAtOnceAndTheResumeOnceTheAudioIsBack() async {
        let rig = CallRig()
        let (call, socket) = await rig.connected()
        rig.system.act(.pause(true), call.id)
        #expect(rig.system.fulfilled.last == .pause(true), "at once: a late or failed pause ends the call")
        #expect(call.held && call.holdReason == .system)
        #expect(call.voice?.held == true && rig.voice.audio.heldNow)
        rig.system.deactivate()
        #expect(rig.voice.audio.deactivations == 1 && call.phase.isLive)
        socket.deliver(data: .pcm(480))
        #expect(rig.voice.audio.played.isEmpty, "nothing plays on hold")
        rig.system.act(.pause(false), call.id)
        #expect(!call.held && call.voice?.held == true, "off hold once the audio is back")
        rig.system.activate()
        #expect(call.voice?.held == false && !rig.voice.audio.heldNow)
    }

    @Test func muteGoesThroughTheSystemAndItsEchoChangesNothing() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        rig.voice.clock.advance(1) // past the engine's start
        rig.calls.setMuted(true)
        await rig.settle()
        #expect(rig.system.requests == [.mute(true)] && !call.muted)
        rig.system.act(.mute(true), call.id)
        #expect(call.muted && call.voice?.muted == true)
        rig.system.act(.mute(true), call.id)
        #expect(rig.system.fulfilled.filter { $0 == .mute(true) }.count == 2 && call.muted)
        // The bar's own Mute on the voice: the system hears it.
        call.voice?.muted = false
        await rig.settle()
        #expect(rig.system.requests.last == .mute(false))
    }

    @Test func aMuteTheSystemRefusesAppliesHere() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        rig.system.requestError = CallSystemStandIn.refused
        rig.calls.setMuted(true)
        await rig.voice.waitUntil { call.muted }
        #expect(call.muted && call.voice?.muted == true)
    }

    /// Mute on "Calling…", before the call's engine was attached and before the system had the conversation (it
    /// refuses the mute then): it applies here; the engine attached after starts muted, so the microphone sends silence
    /// from its first buffer, and the system hears the mute once it has the conversation.
    @Test func aMuteWhileItDialsKeepsTheMicrophoneMuted() async throws {
        let rig = CallRig()
        let call = rig.calls.add(kind: .owner(group: rig.voice.key.group), direction: .outgoing)
        rig.system.requestError = CallSystemStandIn.refused // no conversation yet: nothing to mute
        rig.calls.setMuted(true)
        await rig.voice.waitUntil { call.muted }
        rig.system.requestError = nil
        rig.attachEngine(call)
        await rig.calls.place(call, origin: .app)
        rig.system.act(.start, call.id)
        await rig.voice.waitUntil { rig.voice.socket != nil }
        rig.system.activate()
        let socket = try #require(rig.voice.socket)
        socket.ready(rig.voice.key)
        await rig.voice.waitUntil { call.connectedSince != nil }
        #expect(call.muted && call.voice?.muted == true, "the page says Mute and the voice is muted")
        rig.voice.audio.capture(.pcm(320, 5000))
        #expect(socket.audio.last == Data(count: 640), "the microphone sends silence")
        await rig.settle()
        #expect(rig.system.requests == [.mute(true), .mute(true)], "the system hears the mute once it has the conversation")
        rig.system.act(.mute(true), call.id)
        #expect(call.muted && rig.system.fulfilled.last == .mute(true), "its echo changes nothing")
    }

    @Test func aSystemMuteAsTheEngineStartsIsIgnored() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        rig.system.act(.mute(true), call.id)
        #expect(!call.muted && rig.system.fulfilled.last == .mute(true))
        await rig.settle()
        #expect(rig.system.requests == [.mute(false)], "the owner's mute asserted again, once")
    }

    @Test func noActivationActivatesOnceThenTheCallFails() async {
        let rig = CallRig()
        let call = await rig.dial()
        rig.system.act(.start, call.id)
        await rig.voice.waitUntil { rig.voice.socket != nil }
        rig.system.activates = false
        await rig.voice.advance(5, sleepers: 2)
        await rig.voice.waitUntil { call.phase.isEnded }
        #expect(rig.system.activations == 1)
        #expect(call.phase == .ended(.failed) && rig.system.ended == [.failed])
        #expect(call.voice?.phase.isEnded == true)
    }

    @Test func anInterruptionHoldsAndTheCallStaysWhenTheAudioDoesNotComeBack() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        rig.voice.audio.emit(.interrupted)
        #expect(call.held && call.holdReason == .interruption && call.phase.isLive)
        #expect(call.voice?.phase.isEnded == false)
        rig.system.deactivate()
        rig.system.activates = false
        // Each wait starts only once its timer sleeps (the clock moves only for sleepers already there).
        let sleeping = rig.voice.clock.sleeping
        rig.voice.audio.emit(.interruptionEnded(shouldResume: true))
        await rig.voice.advance(1, sleepers: sleeping + 2)
        await rig.voice.waitUntil { rig.system.requests.contains(.pause(false)) }
        #expect(rig.system.requests.contains(.pause(false)), "the system asked to resume")
        await rig.voice.advance(0.5, sleepers: sleeping + 2)
        await rig.voice.waitUntil { rig.system.activations == 1 }
        #expect(rig.system.activations == 1, "then the session activated once")
        await rig.voice.advance(4, sleepers: sleeping + 1)
        await rig.voice.waitUntil { call.resumeOffered }
        #expect(call.resumeOffered && call.held && call.phase.isLive, "never ended silently")
        rig.system.activate()
        #expect(!call.held && !call.resumeOffered)
    }

    /// The interruption's notifications alone (no `didDeactivate`): the engine went with the session, and the system's
    /// next activation brings it back; the call is never left connected and silent.
    @Test func anInterruptionWithoutADeactivationComesBackOnTheNextActivation() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        #expect(rig.voice.audio.activations == 1)
        rig.voice.audio.interruptSystemSession()
        #expect(call.held && call.holdReason == .interruption && rig.voice.audio.deactivations == 1)
        rig.voice.audio.emit(.interruptionEnded(shouldResume: true))
        rig.system.activate()
        #expect(rig.voice.audio.activations == 2 && rig.voice.audio.active, "the engine starts again")
        #expect(!call.held && call.voice?.held == false && !rig.voice.audio.heldNow)
    }

    @Test func anInterruptionWithoutADeactivationStillRecoversAndOffersResume() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        rig.voice.audio.interruptSystemSession()
        rig.system.activates = false
        let sleeping = rig.voice.clock.sleeping
        rig.voice.audio.emit(.interruptionEnded(shouldResume: true))
        await rig.voice.advance(1, sleepers: sleeping + 2)
        await rig.voice.waitUntil { rig.system.requests.contains(.pause(false)) }
        #expect(rig.system.requests.contains(.pause(false)), "the system asked to resume")
        await rig.voice.advance(0.5, sleepers: sleeping + 2)
        await rig.voice.waitUntil { rig.system.activations == 1 }
        #expect(rig.system.activations == 1, "then the session activated once")
        await rig.voice.advance(4, sleepers: sleeping + 1)
        await rig.voice.waitUntil { call.resumeOffered }
        #expect(call.resumeOffered && call.held && call.phase.isLive)
        rig.system.activate()
        #expect(!call.held && rig.voice.audio.activations == 2)
    }

    /// Start and End (or a reset) one after the other, before the voice's start ran: it never begins, so nothing claims
    /// the microphone or opens a socket after the system call is over.
    @Test func aStartThenAnEndBeforeTheVoiceBeganNeverStartsIt() async {
        for reset in [false, true] {
            let rig = CallRig()
            let call = await rig.dial()
            rig.system.act(.start, call.id)
            if reset { rig.calls.systemReset() } else { rig.system.act(.end, call.id) }
            await rig.settle()
            try? await Task.sleep(for: .milliseconds(50))
            #expect(call.phase.isEnded && call.voice?.phase.isEnded == true, "reset \(reset)")
            #expect(rig.voice.sockets.isEmpty && !rig.voice.audio.running, "reset \(reset): no socket, no audio")
            #expect(rig.voice.microphone.isFree && rig.voiceCalls.current == nil, "reset \(reset)")
        }
    }

    /// Calls to Persona end: their group's tunnels may go (the background kept them for the call).
    @Test func aCallToPersonaEndingLetsItsTunnelsGo() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        #expect(rig.left.isEmpty)
        call.voice?.end(.lost)
        #expect(rig.left == [rig.voice.key.group])
    }

    @Test func aStartTooEarlyIsTriedOnceMoreAndAnotherRefusalFallsBack() async {
        let early = NSError(domain: CallSystemError.requestTransaction, code: 6)
        let rig = CallRig()
        rig.system.startErrors = [early]
        let call = rig.calls.add(kind: .owner(group: "g"), direction: .outgoing)
        let placing = Task { await rig.calls.place(call, origin: .app) }
        await rig.voice.advance(0.5)
        await placing.value
        #expect(rig.system.starts.count == 2 && call.phase.isLive)

        let full = CallRig()
        full.system.startErrors = [NSError(domain: "wrapper", code: 1, userInfo: [NSUnderlyingErrorKey: NSError(domain: CallSystemError.requestTransaction, code: 7)])]
        let refused = full.calls.add(kind: .owner(group: "g"), direction: .outgoing)
        var instead: CallSystemError?
        await full.calls.place(refused, origin: .app) { instead = $0 }
        #expect(instead?.isAtCapacity == true && full.system.starts.count == 1)
        #expect(refused.phase == .ended(.failed) && refused.note == PersonaCalls.insteadLine(instead!))
    }

    @Test func aResetEndsEveryCall() async {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        rig.calls.systemReset()
        #expect(call.phase == .ended(.failed) && call.voice?.phase.isEnded == true)
    }

    // MARK: Urgent calls

    @Test func anUrgentCallRingsWithPersonasHandleAndIsRefusedNothing() async throws {
        let rig = CallRig()
        let id = UUID()
        try await rig.calls.ring(id, callID: "call_1", name: "Urgent: billing")
        #expect(rig.system.rings.map(\.name) == ["Urgent: billing"])
        #expect(rig.calls.urgentInProgress && rig.calls.call(id)?.phase == .ringing)
        rig.system.act(.join, id)
        #expect(rig.system.fulfilled == [.join] && rig.calls.call(id)?.connectedSince != nil)
        rig.calls.urgentEnded(id)
        #expect(rig.system.ended == [.remoteEnded] && !rig.calls.urgentInProgress)
    }

    @Test func answeringAnUrgentCallEndsAVoiceChatFirst() async throws {
        let rig = CallRig()
        await rig.voiceCalls.start(rig.voice.key, extensionID: "voice", surface: .phone, deps: rig.voice.deps)
        let chat = try #require(rig.voiceCalls.current)
        let id = UUID()
        try await rig.calls.ring(id, callID: "call_1", name: "Persona Urgent Call")
        #expect(!chat.phase.isEnded, "a ring alone ends nothing")
        rig.system.act(.join, id)
        #expect(chat.phase == .ended(.urgentCall))
        #expect(VoiceEndNote.make(.urgentCall, extensionID: "voice")?.again == .resume, "Continue offered")
    }

    /// While an urgent call rings, and once it is answered: no voice chat starts and none is picked up again (Continue,
    /// CarPlay's too), and nothing of the audio is touched; once it is over, Continue works.
    @Test func noVoiceChatStartsOrContinuesUnderAnUrgentCall() async throws {
        let rig = CallRig()
        await rig.voiceCalls.start(rig.voice.key, extensionID: "voice", surface: .phone, deps: rig.voice.deps)
        let chat = try #require(rig.voiceCalls.current)
        chat.end(.interrupted)
        let id = UUID()
        try await rig.calls.ring(id, callID: "call_1", name: "Persona Urgent Call")
        let stops = rig.voice.audio.stops
        #expect(await rig.voiceCalls.resume(rig.voice.key) == .urgentCall, "ringing: Continue is refused")
        let other = SessionKey(group: rig.voice.key.group, machineID: "m2", sessionID: "s2")
        #expect(await rig.voiceCalls.start(other, extensionID: "voice", surface: .phone, deps: rig.voice.deps) == .urgentCall)
        #expect(rig.voiceCalls.current === chat && chat.phase == .ended(.interrupted))
        #expect(rig.voice.sockets.count == 1 && !rig.voice.audio.running && rig.voice.audio.stops == stops, "no audio touched")
        rig.system.act(.join, id)
        #expect(await rig.voiceCalls.start(other, extensionID: "voice", surface: .carPlay, deps: rig.voice.deps) == .urgentCall,
                "answered: CarPlay's start is refused")
        rig.calls.urgentEnded(id)
        #expect(await rig.voiceCalls.resume(rig.voice.key) == nil)
        #expect(chat.phase == .connecting && rig.voice.sockets.count == 2)
    }

    /// CarPlay's chat ended for the urgent call offers Continue; it is refused until the urgent call is over.
    @Test func carPlaysContinueAfterTheUrgentCallWaitsForItsEnd() async throws {
        let rig = CallRig()
        await rig.voiceCalls.start(rig.voice.key, extensionID: "voice", surface: .carPlay, deps: rig.voice.deps)
        let chat = try #require(rig.voiceCalls.current)
        let id = UUID()
        try await rig.calls.ring(id, callID: "call_1", name: "Persona Urgent Call")
        rig.system.act(.join, id)
        #expect(chat.phase == .ended(.urgentCall))
        #expect(await rig.voiceCalls.resume(rig.voice.key) == .urgentCall)
        #expect(chat.phase == .ended(.urgentCall) && rig.voice.sockets.count == 1 && !rig.voice.audio.running)
        rig.calls.urgentEnded(id)
        #expect(await rig.voiceCalls.resume(rig.voice.key) == nil && chat.phase == .connecting)
    }

    /// An urgent call that rang while the call to Persona was being prepared (the hub asked, the microphone prompt):
    /// asked again just before the system's Start, and refused with the urgent call's note.
    @Test func anUrgentCallThatRangMeanwhileRefusesTheStart() async throws {
        let rig = CallRig()
        let call = rig.calls.add(kind: .owner(group: "g"), direction: .outgoing)
        try await rig.calls.ring(UUID(), callID: "call_1", name: "Persona Urgent Call")
        await rig.calls.place(call, origin: .app)
        #expect(rig.system.starts.isEmpty)
        #expect(call.phase == .ended(.failed) && rig.calls.refusal == PersonaCalls.urgentNote && !rig.calls.presented)
    }

    @Test func holdAndAcceptThenTheUrgentCallEndsAndTheCallToPersonaResumes() async throws {
        let rig = CallRig()
        let (call, _) = await rig.connected()
        let id = UUID()
        try await rig.calls.ring(id, callID: "call_1", name: "Persona Urgent Call")
        // Hold & Accept: Pause for the call to Persona and Join for the urgent call, together.
        rig.system.act(.pause(true), call.id)
        rig.system.act(.join, id)
        rig.system.deactivate()
        #expect(call.held && call.voice?.held == true && call.phase.isLive)
        rig.system.activate()
        #expect(call.held, "the activation was the urgent call's")
        rig.system.act(.end, id)
        await rig.settle()
        #expect(rig.system.requests.contains(.pause(false)), "the system does not resume our own held call by itself")
    }

    @Test func anEndForACallItDoesNotHaveIsHarmless() {
        let rig = CallRig()
        rig.system.handler = rig.calls
        rig.system.act(.end, UUID())
        rig.system.act(.start, UUID())
        #expect(rig.system.fulfilled == [.end] && rig.system.failed == [.start])
    }

    @Test func whichVoiceEndingsAreFailures() {
        #expect(!VoiceEnd.idle.failsCall && !VoiceEnd.user.failsCall && !VoiceEnd.microphoneTaken.failsCall && !VoiceEnd.replaced.failsCall)
        #expect(VoiceEnd.lost.failsCall && VoiceEnd.audioFailed.failsCall && VoiceEnd.rejected(status: 401).failsCall)
        #expect(VoiceEnd.failed(code: nil, message: "x").failsCall && VoiceEnd.noHub.failsCall)
    }

    @Test func theSystemsErrorsAreReadByTheirDomain() {
        let wrapped = NSError(domain: "LCK", code: 1, userInfo: [NSUnderlyingErrorKey: NSError(domain: CallSystemError.requestTransaction, code: 6)])
        #expect(CallSystemError(wrapped).isTooEarly)
        #expect(!CallSystemError(NSError(domain: NSURLErrorDomain, code: 6)).isTooEarly)
    }

    @Test func aRedialFromRecentsIsACall() {
        #expect(PersonaCalls.isCall(NSUserActivity(activityType: "INStartCallIntent")))
        #expect(PersonaCalls.isCall(NSUserActivity(activityType: "INStartAudioCallIntent")))
        #expect(!PersonaCalls.isCall(NSUserActivity(activityType: "com.apple.corespotlightitem")))
    }

    #if DEBUG
    @Test func everySampleShowsWithoutTheSystem() {
        let rig = CallRig()
        for sample in PersonaCalls.Sample.allCases {
            rig.calls.showSample(sample)
            #expect(rig.calls.presented)
        }
        rig.calls.showSample(.resume)
        #expect(rig.calls.ownerCall?.resumeOffered == true)
        rig.calls.showSample(.held)
        #expect(rig.calls.ownerCall?.holdReason == .system)
        rig.calls.showSample(.connected)
        #expect(rig.calls.ownerCall?.connectedSince == rig.voice.clock.now.addingTimeInterval(-42))
        #expect(!rig.calls.systemMade && rig.system.reports.isEmpty)
    }
    #endif
}

nonisolated enum CallSystemStandIn: Error {
    case refused
}
