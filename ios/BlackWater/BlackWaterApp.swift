//  BlackWaterApp.swift
//
//  The container app exists because iOS requires the Share Extension to be
//  bundled inside a full app. It has no real UI beyond an instructions
//  screen — the actual work happens in BlackWaterShareExtension.

import SwiftUI

@main
struct BlackWaterApp: App {
    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}
