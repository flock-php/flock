import AppKit
import SwiftUI
import WebKit

/// Renders an HTML string. Mail HTML runs with JavaScript off and remote loads blocked
/// (no tracking pixels, no surprises); dumps need JavaScript for Symfony's collapsible output.
struct WebView: NSViewRepresentable {
    let html: String
    var allowsJavaScript = false
    var blocksRemoteContent = true

    func makeCoordinator() -> Coordinator {
        Coordinator()
    }

    func makeNSView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.defaultWebpagePreferences.allowsContentJavaScript = allowsJavaScript
        configuration.websiteDataStore = .nonPersistent()

        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = context.coordinator
        webView.setValue(false, forKey: "drawsBackground")
        context.coordinator.load(html, into: webView, blockingRemote: blocksRemoteContent)
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        context.coordinator.load(html, into: webView, blockingRemote: blocksRemoteContent)
    }

    @MainActor
    final class Coordinator: NSObject, WKNavigationDelegate {
        private var loadedHTML: String?

        func load(_ html: String, into webView: WKWebView, blockingRemote: Bool) {
            guard html != loadedHTML else { return }
            loadedHTML = html

            Task { @MainActor in
                if blockingRemote, let rules = await RemoteContentBlocker.rules() {
                    webView.configuration.userContentController.removeAllContentRuleLists()
                    webView.configuration.userContentController.add(rules)
                }
                webView.loadHTMLString(html, baseURL: nil)
            }
        }

        /// Links open in the default browser instead of inside the viewer.
        func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction) async -> WKNavigationActionPolicy {
            guard navigationAction.navigationType == .linkActivated, let url = navigationAction.request.url else {
                return .allow
            }
            NSWorkspace.shared.open(url)
            return .cancel
        }
    }
}

/// A compiled WebKit content rule list that blocks every http(s) load.
@MainActor
enum RemoteContentBlocker {
    private static var compiled: WKContentRuleList?

    static func rules() async -> WKContentRuleList? {
        if let compiled {
            return compiled
        }
        let json = #"[{"trigger":{"url-filter":"^https?://"},"action":{"type":"block"}}]"#
        compiled = try? await WKContentRuleListStore.default().compileContentRuleList(
            forIdentifier: "dev.flock.block-remote", encodedContentRuleList: json)
        return compiled
    }
}
