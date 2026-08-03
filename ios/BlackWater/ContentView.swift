//  ContentView.swift
//
//  Standalone this screen does nothing useful — its whole job is to say
//  "the tool you want is in the share sheet, not here." Also shows the
//  inbox count so you can eyeball whether iCloud sync is working before
//  you go looking on the Mac.

import SwiftUI

struct ContentView: View {
    @State private var inboxCount: Int = 0

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 16) {
                Text("BlackWater")
                    .font(.largeTitle)
                    .fontWeight(.bold)

                Text("Personal MoMo / bank SMS → ledger.")
                    .foregroundStyle(.secondary)

                Divider().padding(.vertical, 8)

                Text("How to use")
                    .font(.headline)

                stepRow(number: 1, text: "Open Messages and long-press a MoMo/bank SMS.")
                stepRow(number: 2, text: "Tap Share, then BlackWater.")
                stepRow(number: 3, text: "Confirm the sender and save.")

                Divider().padding(.vertical, 8)

                HStack {
                    Image(systemName: "tray")
                    Text("Inbox: \(inboxCount) message\(inboxCount == 1 ? "" : "s") pending ingest")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }

                Text("Files sync via iCloud Drive. Run `ledger ingest` on your Mac to pull them into the ledger DB.")
                    .font(.footnote)
                    .foregroundStyle(.tertiary)

                Spacer()
            }
            .padding()
            .onAppear(perform: refreshInboxCount)
        }
    }

    private func stepRow(number: Int, text: String) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Text("\(number).")
                .fontWeight(.semibold)
                .frame(width: 20, alignment: .leading)
            Text(text)
        }
    }

    private func refreshInboxCount() {
        inboxCount = InboxStore.pendingCount()
    }
}

#Preview {
    ContentView()
}
