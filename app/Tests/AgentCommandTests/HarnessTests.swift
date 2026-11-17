import Testing

@testable import AgentCommand

@Test func harnessesHaveTheirDaemonNames() {
    #expect(Harness.allCases.map(\.rawValue) == ["claude", "antigravity", "terminal"])
    #expect(Harness.allCases.map(\.title) == ["Claude", "Antigravity", "Terminal"])
    #expect(Harness.allCases.map(\.id) == ["claude", "antigravity", "terminal"])
    #expect(Harness.defaultForNew == .claude, "⌘N starts Claude")
    #expect(Harness.terminal.panelPrompt == "Open Terminal")
    #expect(Harness.antigravity.panelPrompt == "Start Session" && Harness.claude.panelPrompt == "Start Session")
    #expect(Harness(session: "") == .claude && Harness(session: "antigravity") == .antigravity)
    #expect(Harness(session: "codex") == nil)
}

@Test func thePickerMovesAndPicks() {
    var choice = HarnessChoice()
    #expect(choice.matches == [.claude, .antigravity, .terminal])
    #expect(choice.chosen == .claude, "the default comes first")
    choice.move(1)
    #expect(choice.chosen == .antigravity)
    choice.move(1)
    choice.move(1)
    #expect(choice.chosen == .claude, "it wraps around")
    choice.move(-1)
    #expect(choice.chosen == .terminal)
    choice.select(.antigravity)
    #expect(choice.chosen == .antigravity, "the mouse moves it too")
    #expect(choice.number(of: .claude) == 1 && choice.number(of: .terminal) == 3)
}

@Test func typingNarrowsThePicker() {
    var choice = HarnessChoice()
    choice.move(2)
    #expect(choice.setSearch("ANTI") == nil)
    #expect(choice.matches == [.antigravity] && choice.chosen == .antigravity, "the selection starts over")
    #expect(choice.number(of: .antigravity) == 2, "rows keep their numbers")
    #expect(choice.setSearch("zz") == nil)
    #expect(choice.matches.isEmpty && choice.chosen == nil)
    choice.move(1)
    #expect(choice.chosen == nil, "nothing to move through")
    choice.select(.claude)
    #expect(choice.chosen == nil)
    #expect(choice.setSearch("") == nil && choice.matches.count == 3)
}

@Test func aNumberPicksStraightAway() {
    var choice = HarnessChoice()
    #expect(choice.setSearch("1") == .claude)
    #expect(choice.setSearch("2") == .antigravity)
    #expect(choice.setSearch("3") == .terminal)
    #expect(choice.search.isEmpty, "the number isn't kept as a search")
    #expect(choice.setSearch("4") == nil && choice.matches.isEmpty)
    #expect(HarnessChoice([.terminal]).matches == [.terminal])
}
