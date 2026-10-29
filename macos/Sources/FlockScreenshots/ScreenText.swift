import AppKit
import Vision

/// Finds text on a window by reading it (Vision), where views don't tell what they show.
@MainActor
enum ScreenText {
    /// Where the window shows the text, in window coordinates; nil when it doesn't.
    static func find(_ text: String, in window: NSWindow) throws -> NSRect? {
        guard let view = window.contentView, let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return nil }
        view.cacheDisplay(in: view.bounds, to: rep)
        guard let image = rep.cgImage else { return nil }

        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        request.usesLanguageCorrection = false
        request.recognitionLanguages = ["en-US"]
        try VNImageRequestHandler(cgImage: image).perform([request])

        // "…" may be read as "..." or not at all.
        let wanted = text.replacingOccurrences(of: "…", with: "")
        for observation in request.results ?? [] {
            guard let candidate = observation.topCandidates(1).first,
                  let range = candidate.string.replacingOccurrences(of: "...", with: "…").range(of: wanted),
                  let box = try? candidate.boundingBox(for: range)?.boundingBox else { continue }
            // Normalized, from the bottom left; SwiftUI's views count from the top.
            let y = view.isFlipped ? 1 - box.maxY : box.minY
            let rect = NSRect(x: box.minX * view.bounds.width, y: y * view.bounds.height,
                              width: box.width * view.bounds.width, height: box.height * view.bounds.height)
            return view.convert(rect, to: nil)
        }
        return nil
    }
}
