import Foundation
import Testing
@testable import Persona

/// The call to Persona's page (#9290, docs/design/DESIGN_SYSTEM.md "Call screen"): its one line and its orb from the
/// call's state, the page shown again when the app comes back, Minimise never ending the call, the + panel's Call
/// Persona beside Voice Chat, and every control going through `PersonaCalls` (read from the sources).
@MainActor
struct CallScreenTests {
    private static let since = Date(timeIntervalSinceReferenceDate: 1_000)

    private static func state(_ phase: CallPhase, held: Bool = false, muted: Bool = false, note: String? = nil,
                              voice: (VoicePhase, VoiceLink)? = nil, reconnecting: Bool = false) -> CallScreenState {
        CallScreenState.make(phase: phase, held: held, muted: muted, note: note, voice: voice.map { (phase: $0.0, link: $0.1) },
                             reconnecting: reconnecting)
    }

    @Test func theLineSaysWhereTheCallIs() {
        #expect(Self.state(.dialing) == CallScreenState(line: .calling, orb: .connecting))
        #expect(Self.state(.connecting) == CallScreenState(line: .connecting, orb: .connecting))
        #expect(Self.state(.connected(since: Self.since), voice: (.live(.listening), .live)).line == .duration(since: Self.since))
        #expect(Self.state(.ended(.failed), note: "Can't reach the hub") == CallScreenState(line: .ended(note: "Can't reach the hub"), orb: .ended))
        #expect(Self.state(.ended(.hungUp)).line == .ended(note: nil))
    }

    /// On hold wins over everything else the voice says; the voice's connection being picked up again shows as
    /// Reconnecting…, from its phase or its link (a sample has neither: its flag stands in).
    @Test func onHoldAndReconnectingTakeTheLine() {
        let on = CallPhase.connected(since: Self.since)
        #expect(Self.state(on, held: true, voice: (.reconnecting(attempt: 1), .live)) == CallScreenState(line: .onHold, orb: .paused))
        #expect(Self.state(on, voice: (.reconnecting(attempt: 2), .live)) == CallScreenState(line: .reconnecting, orb: .connecting))
        #expect(Self.state(on, voice: (.live(.listening), .reconnecting)).line == .reconnecting)
        #expect(Self.state(on, reconnecting: true).line == .reconnecting)
        #expect(Self.state(on).line == .duration(since: Self.since))
    }

    /// The orb moves as the voice bar's does: speaking, hearing, muted, the upstream paused.
    @Test func theOrbFollowsTheVoice() {
        let on = CallPhase.connected(since: Self.since)
        #expect(Self.state(on, voice: (.live(.speaking), .live)).orb == .speaking)
        #expect(Self.state(on, voice: (.live(.hearing), .live)).orb == .hearing)
        #expect(Self.state(on, muted: true, voice: (.live(.listening), .live)).orb == .muted)
        #expect(Self.state(on, voice: (.live(.listening), .paused)).orb == .paused)
        #expect(Self.state(on, muted: true).orb == .muted, "a sample, no voice")
        #expect(Self.state(on).orb == .listening)
    }

    /// Minimise only hides the page; coming back to the app (the Dynamic Island, the Lock Screen's call) shows it again
    /// while the call is on, and never brings back a call that is over or an urgent call.
    @Test func comingBackToTheAppShowsTheCallAgain() async {
        let rig = CallRig()
        let (call, socket) = await rig.connected()
        rig.calls.presented = true
        rig.calls.minimise()
        #expect(!rig.calls.presented && call.phase.isLive && !socket.closed && rig.system.ended.isEmpty)
        rig.calls.returnedToApp()
        #expect(rig.calls.presented)
        rig.calls.minimise()
        rig.calls.hangUp()
        rig.system.act(.end, call.id)
        #expect(call.phase.isEnded)
        rig.calls.returnedToApp()
        #expect(!rig.calls.presented, "an ended call's page does not come back")
    }

    @Test func anUrgentCallIsNotTheCallScreens() async throws {
        let rig = CallRig()
        try await rig.calls.ring(UUID(), callID: "call_1", name: "Urgent")
        #expect(rig.calls.ownerCall == nil)
        rig.calls.returnedToApp()
        rig.calls.present()
        #expect(!rig.calls.presented)
    }

    /// Reading what the cover and the bar read makes no system call manager (#9264): only a call does.
    @Test func theCoverMakesNoCallManager() {
        var made = 0
        let calls = PersonaCalls(system: { made += 1; return FakeCallSystem() }, voiceCalls: VoiceCalls())
        _ = calls.presented
        _ = calls.ownerCall
        _ = calls.refusal
        _ = calls.route.external
        _ = calls.route.speakerOn
        calls.returnedToApp()
        calls.minimise()
        #expect(made == 0 && !calls.systemMade)
    }

    /// The + panel's capture rows: Call Persona right after Voice Chat, and not there where the page offers none (an
    /// Agent's chat, another assistant conversation).
    @Test func callPersonaSitsRightAfterVoiceChat() throws {
        let rows = AttachMoreSection.common(voice: {}, call: {}, camera: {}, glasses: nil, mention: {})
        let capture = try #require(rows.first { $0.id == "capture" })
        #expect(capture.rows.map(\.id).prefix(3) == ["composer-menu-voice", "composer-menu-call", "composer-menu-camera"])
        #expect(capture.rows[1].symbol == Theme.Icon.callPersona && capture.rows[1].symbol != capture.rows[0].symbol)
        let agent = AttachMoreSection.common(voice: {}, camera: {}, glasses: nil, mention: {})
        #expect(agent.flatMap(\.rows).allSatisfy { $0.id != "composer-menu-call" })
    }

    /// The orb gives way on a short page or at a large text size, never below the voice bar's orb.
    @Test func theOrbGivesWayToTheControls() {
        #expect(CallScreen.orbSize(height: 0, scale: 1) == Theme.Size.callOrb)
        #expect(CallScreen.orbSize(height: 900, scale: 1) == Theme.Size.callOrb)
        let large = CallScreen.orbSize(height: 740, scale: 1.4)
        #expect(large * 1.4 <= 740 * Theme.Size.callOrbShare + 0.01 && large < Theme.Size.callOrb)
        #expect(CallScreen.orbSize(height: 100, scale: 1) == Theme.Size.voiceOrb)
    }

    @Test func theAudioControlNamesTheRoute() {
        #expect(CallAudioRouteControl.name(.bluetooth("AirPods Pro")) == "AirPods Pro")
        #expect(!CallAudioRouteControl.name(.speaker).isEmpty && !CallAudioRouteControl.name(.receiver).isEmpty)
        #expect(CallAudioRouteControl.name(.speaker) != CallAudioRouteControl.name(.receiver))
    }

    /// A round control fills only while its toggle is on.
    @Test func onlyAToggleThatIsOnIsOn() {
        #expect(CallControlKind.toggle(on: true).isOn)
        #expect(!CallControlKind.toggle(on: false).isOn && !CallControlKind.action.isOn && !CallControlKind.end.isOn)
    }

    // MARK: The sources

    private static let apple = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()

    private static func code(_ path: String) throws -> String {
        try String(contentsOf: apple.appending(path: path), encoding: .utf8).components(separatedBy: "\n")
            .map { l in l.range(of: "//").map { String(l[..<$0.lowerBound]) } ?? l }
            .joined(separator: "\n")
    }

    /// The call screen and its controls act only through `PersonaCalls` (so the system's call UI and AirPods say the
    /// same): never on the voice's mute, its hold, the in-app calls or the audio session themselves.
    @Test func theCallScreenActsThroughPersonaCalls() throws {
        for file in ["Persona/Calls/CallScreen.swift", "Persona/Calls/CallControls.swift"] {
            let code = try Self.code(file)
            for direct in ["VoiceCalls", "setCallMuted", "setHeld(", "muted.toggle", "muted = ", "overrideOutputAudioPort",
                           "AVAudioSession", ".end(."] {
                #expect(!code.contains(direct), "\(file) uses \(direct)")
            }
        }
        let screen = try Self.code("Persona/Calls/CallScreen.swift")
        for action in ["calls.setMuted(", "calls.setSpeaker(", "calls.resume(", "calls.hangUp(", "calls.minimise()", "calls.present()",
                       "calls.returnedToApp()"] {
            #expect(screen.contains(action), "the call screen has no \(action)")
        }
    }

    /// A system call's voice bar hangs up, mutes and expands through `PersonaCalls`; its ended line calls Persona again.
    @Test func aSystemCallsBarAndEndedLineGoThroughPersonaCalls() throws {
        let bar = try Self.code("Persona/Views/Voice/VoiceBar.swift")
        #expect(bar.contains("if call.surface == .call { return PersonaCalls.shared.setMuted(!call.muted) }"))
        #expect(bar.contains("if call.surface == .call { return PersonaCalls.shared.hangUp() }"))
        #expect(bar.contains("PersonaCalls.shared.present()"))
        let strip = try Self.code("Persona/Views/Voice/VoiceEntry.swift")
        #expect(strip.contains("PersonaCalls.shared.callPersona(group: group, origin: .app)"))
    }

    /// The page is the app's, over everything, next to the urgent call's cover.
    @Test func theCoverIsTheAppsNextToTheUrgentCalls() throws {
        let app = try Self.code("Persona/App/PersonaApp.swift").components(separatedBy: "\n").map { $0.trimmingCharacters(in: .whitespaces) }
        let cover = try #require(app.firstIndex(of: ".personaCallCover()"))
        #expect(app[cover + 1] == ".urgentCallCover()")
    }
}
