//  ShareViewController.swift
//
//  Appears in the iOS share sheet when you long-press an SMS in Messages
//  and tap Share. Presents a sender picker + body editor, then writes a
//  JSON file into the shared iCloud Drive inbox. Nothing here talks to
//  the network.
//
//  IMPORTANT: this file depends on InboxStore.swift being added to BOTH
//  the container app target AND this extension target. In Xcode's File
//  Inspector for InboxStore.swift, check both targets under "Target
//  Membership". Otherwise the extension won't be able to write the file.

import UIKit
import SwiftUI
import UniformTypeIdentifiers

/// Common Ghanaian MoMo / bank sender IDs. Add yours if it's missing.
/// The last-picked sender is remembered so the next share is one tap.
private let knownSenders: [String] = [
    "MobileMoney",
    "MTN",
    "TelecelCash",
    "Telecel",
    "Ecobank",
    "GCB",
    "Fidelity",
    "Absa",
    "Stanbic",
    "AT",
]

private let lastSenderKey = "blackwater.lastSender"

class ShareViewController: UIViewController {

    override func viewDidLoad() {
        super.viewDidLoad()
        extractSharedText { [weak self] text in
            guard let self else { return }
            DispatchQueue.main.async {
                self.presentEditor(prefilledBody: text ?? "")
            }
        }
    }

    // MARK: - Text extraction

    /// Pulls plain text out of the share item, whatever wrapper it came in.
    /// Messages hands us `public.text` when you share a single SMS.
    private func extractSharedText(completion: @escaping (String?) -> Void) {
        guard
            let item = extensionContext?.inputItems.first as? NSExtensionItem,
            let providers = item.attachments
        else {
            completion(nil)
            return
        }
        for provider in providers {
            if provider.hasItemConformingToTypeIdentifier(UTType.text.identifier) {
                provider.loadItem(forTypeIdentifier: UTType.text.identifier, options: nil) { data, _ in
                    if let s = data as? String {
                        completion(s)
                    } else if let d = data as? Data, let s = String(data: d, encoding: .utf8) {
                        completion(s)
                    } else {
                        completion(nil)
                    }
                }
                return
            }
        }
        completion(nil)
    }

    // MARK: - Editor

    private func presentEditor(prefilledBody: String) {
        let editor = InboxEditor(
            initialBody: prefilledBody,
            initialSender: UserDefaults.standard.string(forKey: lastSenderKey) ?? knownSenders[0],
            onSave: { [weak self] sender, body in
                self?.persist(sender: sender, body: body)
            },
            onCancel: { [weak self] in
                self?.finish()
            }
        )
        let host = UIHostingController(rootView: editor)
        addChild(host)
        host.view.frame = view.bounds
        host.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(host.view)
        host.didMove(toParent: self)
    }

    private func persist(sender: String, body: String) {
        do {
            _ = try InboxStore.save(sender: sender, body: body)
            UserDefaults.standard.set(sender, forKey: lastSenderKey)
            finish()
        } catch {
            let alert = UIAlertController(
                title: "Could not save",
                message: error.localizedDescription,
                preferredStyle: .alert
            )
            alert.addAction(UIAlertAction(title: "OK", style: .default) { [weak self] _ in
                self?.finish()
            })
            present(alert, animated: true)
        }
    }

    private func finish() {
        extensionContext?.completeRequest(returningItems: nil)
    }
}

// MARK: - SwiftUI editor

private struct InboxEditor: View {
    let initialBody: String
    let initialSender: String
    let onSave: (_ sender: String, _ body: String) -> Void
    let onCancel: () -> Void

    @State private var sender: String
    @State private var body: String

    init(
        initialBody: String,
        initialSender: String,
        onSave: @escaping (String, String) -> Void,
        onCancel: @escaping () -> Void
    ) {
        self.initialBody = initialBody
        self.initialSender = initialSender
        self.onSave = onSave
        self.onCancel = onCancel
        _sender = State(initialValue: initialSender)
        _body = State(initialValue: initialBody)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Sender") {
                    Picker("Sender", selection: $sender) {
                        ForEach(knownSenders, id: \.self) { s in Text(s).tag(s) }
                    }
                    .pickerStyle(.menu)
                    TextField("Or type a custom sender", text: $sender)
                        .autocapitalization(.none)
                        .autocorrectionDisabled(true)
                }
                Section("Message") {
                    TextEditor(text: $body)
                        .frame(minHeight: 140)
                        .autocorrectionDisabled(true)
                }
            }
            .navigationTitle("Save to BlackWater")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onCancel)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { onSave(sender.trimmingCharacters(in: .whitespacesAndNewlines), body) }
                        .disabled(body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
                                  sender.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
    }
}
