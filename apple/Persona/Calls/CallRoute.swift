#if os(iOS)
import AVFoundation
import Foundation
import Observation
import os

/// Where a system call's sound goes now (#9290), for the call page: read from the audio session each time its route
/// changes, never from what the app asked for (the owner can pick another one in Control Center or the system's call
/// UI). Watched only while there is a call; Speaker asks the session for the loudspeaker, or gives the route back.
@MainActor @Observable final class CallRoute {
    nonisolated enum Output: Equatable, Sendable {
        /// The earpiece: a call's own route with nothing connected.
        case receiver
        case speaker
        /// Bluetooth (hands-free, A2DP or LE), by the device's name as the system shows it.
        case bluetooth(String)
        /// Wired headphones or a headset.
        case headphones
        case car
        case other
    }

    private(set) var output: Output = .receiver
    var speakerOn: Bool { output == .speaker }

    @ObservationIgnored private var observer: NSObjectProtocol?
    @ObservationIgnored private var overrode = false
    nonisolated private static let log = Logger(subsystem: "persona", category: "call")

    /// The route as the session has it, by its first output.
    nonisolated static func output(_ route: AVAudioSessionRouteDescription) -> Output {
        guard let port = route.outputs.first else { return .receiver }
        return output(port.portType, name: port.portName)
    }

    nonisolated static func output(_ port: AVAudioSession.Port, name: String) -> Output {
        switch port {
        case .builtInReceiver: .receiver
        case .builtInSpeaker: .speaker
        case .bluetoothHFP, .bluetoothA2DP, .bluetoothLE: .bluetooth(name)
        case .headphones, .headsetMic: .headphones
        case .carAudio: .car
        default: .other
        }
    }

    /// From the first call on: the route as it is, and again on every change.
    func watch() {
        guard observer == nil else { return }
        // Read where the session posts it, off the main actor; only the answer comes over.
        observer = NotificationCenter.default.addObserver(forName: AVAudioSession.routeChangeNotification, object: nil,
                                                          queue: nil) { @Sendable [weak self] _ in
            let now = Self.output(AVAudioSession.sharedInstance().currentRoute)
            Task { @MainActor in self?.set(now) }
        }
        Task.detached { [weak self] in
            let now = Self.output(AVAudioSession.sharedInstance().currentRoute)
            await MainActor.run { self?.set(now) }
        }
    }

    /// The last call ended: the speaker given back (the next recording or dictation starts from the system's route).
    func unwatch() {
        if let observer { NotificationCenter.default.removeObserver(observer) }
        observer = nil
        if overrode { setSpeaker(false) }
    }

    /// Speaker on: the loudspeaker; off: the call's route (the earpiece, or what is connected).
    func setSpeaker(_ on: Bool) {
        overrode = on
        Task.detached {
            do {
                try AVAudioSession.sharedInstance().overrideOutputAudioPort(on ? .speaker : .none)
            } catch {
                Self.log.notice("call: could not \(on ? "turn the speaker on" : "turn the speaker off", privacy: .public): \(error.localizedDescription, privacy: .public)")
            }
        }
    }

    private func set(_ now: Output) {
        if output != now { output = now }
    }

    #if DEBUG
    /// The call page's samples.
    func show(_ sample: Output) { set(sample) }
    #endif
}
#endif
