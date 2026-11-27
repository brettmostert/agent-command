import AppKit
import Testing

@testable import AgentCommand

@Test func groupColorsResolveInThreeTiers() {
    #expect(GroupColor.resolve("blue", isDark: true) == .theme(4))
    #expect(GroupColor.resolve("bright-red", isDark: true) == .theme(9))
    #expect(GroupColor.resolve("fixed-orange", isDark: true) == .rgb(red: 0xF9, green: 0x91, blue: 0x57))
    #expect(GroupColor.resolve("fixed-orange", isDark: false) == .rgb(red: 0xD3, green: 0x54, blue: 0x00), "a light theme's value")
    #expect(GroupColor.resolve("#0A0B0C", isDark: false) == .rgb(red: 10, green: 11, blue: 12))
    for bad in ["mauve", "bright-teal", "fixed-mauve", "#12345", "#GGGGGG", ""] {
        #expect(GroupColor.resolve(bad, isDark: true) == .unknown, "\(bad)")
    }
    #expect(GroupColor.themeNames.count == 12 && GroupColor.themeNames.first == "blue" && GroupColor.themeNames[6] == "bright-blue")
    #expect(GroupColor.fixed.count == 12)
}

@Test func groupColorsAreNamedAndStored() {
    #expect(GroupColor.label("bright-blue") == "Bright Blue")
    #expect(GroupColor.label("fixed-orange") == "Orange")
    #expect(GroupColor.label("#A1B2C3") == "#A1B2C3")
    #expect(GroupColor.hex(NSColor(srgbRed: 1, green: 0.5, blue: 0, alpha: 1)) == "#FF8000")
}

@Test func anUnarchivedGroupGetsAFreeName() {
    #expect(SessionGroup.freeName("Backend", taken: ["Docs"]) == "Backend")
    #expect(SessionGroup.freeName("Backend", taken: ["backend"]) == "Backend 2")
    #expect(SessionGroup.freeName("Backend", taken: ["Backend", "Backend 2"]) == "Backend 3")
    #expect(SessionGroup.freeName("Backend 2", taken: ["Backend 2"]) == "Backend 3", "carries on from its number")
}
