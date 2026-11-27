import AppKit
import SwiftUI

/// A group's colour, in three tiers (internal/core/colors.go, PAN-190):
/// the theme's 12, then 12 fixed colours with a dark and a light value,
/// then a custom #RRGGBB.
nonisolated enum GroupColor: Equatable {
    /// The theme's ANSI colour at this palette index (1–6, bright 9–14).
    case theme(Int)
    /// An exact colour.
    case rgb(red: Int, green: Int, blue: Int)
    /// Not one the app knows: drawn in the foreground.
    case unknown

    private static let ansiNames = ["red", "green", "yellow", "blue", "magenta", "cyan"]

    /// The theme's 12, in the order new groups take them (core.ThemeColors).
    static let themeNames: [String] = {
        let six = ["blue", "magenta", "cyan", "yellow", "green", "red"]
        return six + six.map { "bright-" + $0 }
    }()

    /// The 12 fixed colours (core.FixedColors): name, dark value, light value.
    static let fixed: [(name: String, dark: String, light: String)] = [
        ("red", "#F2777A", "#C0392B"), ("orange", "#F99157", "#D35400"), ("amber", "#FFB454", "#B7791F"),
        ("yellow", "#FFE066", "#9A7D0A"), ("lime", "#B5E655", "#5E8C10"), ("green", "#66CC88", "#1E8449"),
        ("teal", "#4DD0C0", "#138D75"), ("cyan", "#66D9EF", "#0E7C9B"), ("blue", "#6699FF", "#1F5FBF"),
        ("indigo", "#8C8CFF", "#3F3FBF"), ("purple", "#C594F5", "#7D3C98"), ("pink", "#F58CC8", "#B03A7A"),
    ]

    /// What a stored colour means, on a dark or a light theme.
    static func resolve(_ name: String, isDark: Bool) -> GroupColor {
        if let i = ansiNames.firstIndex(of: name) { return .theme(i + 1) }
        if name.hasPrefix("bright-"), let i = ansiNames.firstIndex(of: String(name.dropFirst(7))) { return .theme(i + 9) }
        if name.hasPrefix("fixed-"), let f = fixed.first(where: { "fixed-" + $0.name == name }) {
            return rgb(isDark ? f.dark : f.light) ?? .unknown
        }
        return rgb(name) ?? .unknown
    }

    /// A #RRGGBB colour, or nil.
    static func rgb(_ hex: String) -> GroupColor? {
        guard hex.count == 7, hex.first == "#", let v = Int(hex.dropFirst(), radix: 16) else { return nil }
        return .rgb(red: v >> 16 & 0xFF, green: v >> 8 & 0xFF, blue: v & 0xFF)
    }

    /// The stored form of an exact colour, for Custom….
    static func hex(_ color: NSColor) -> String {
        let c = color.usingColorSpace(.sRGB) ?? color
        let v = [c.redComponent, c.greenComponent, c.blueComponent].map { Int(($0 * 255).rounded()).clamped(0, 255) }
        return String(format: "#%02X%02X%02X", v[0], v[1], v[2])
    }

    /// How a menu names a colour: "Blue", "Bright Blue", "Orange", "#A1B2C3".
    static func label(_ name: String) -> String {
        if name.hasPrefix("#") { return name }
        return name.replacingOccurrences(of: "fixed-", with: "").replacingOccurrences(of: "-", with: " ").capitalized
    }

    @MainActor func nsColor(theme: AppTheme) -> NSColor {
        switch self {
        case .theme(let i): theme.ansiColor(i)
        case .rgb(let r, let g, let b): NSColor(srgbRed: CGFloat(r) / 255, green: CGFloat(g) / 255, blue: CGFloat(b) / 255, alpha: 1)
        case .unknown: theme.foreground
        }
    }

    /// A stored colour, drawn on theme.
    @MainActor static func color(_ name: String, theme: AppTheme) -> Color {
        Color(nsColor: resolve(name, isDark: theme.isDark).nsColor(theme: theme))
    }

    /// A dot of a colour for a menu item: menus draw SF Symbols in one tint,
    /// so it's an image that keeps its colour.
    @MainActor static func swatch(_ name: String, theme: AppTheme) -> NSImage {
        let fill = resolve(name, isDark: theme.isDark).nsColor(theme: theme)
        let image = NSImage(size: NSSize(width: 12, height: 12), flipped: false) { rect in
            fill.setFill()
            NSBezierPath(ovalIn: rect.insetBy(dx: 1, dy: 1)).fill()
            return true
        }
        image.isTemplate = false
        return image
    }
}

nonisolated private extension Int {
    func clamped(_ low: Int, _ high: Int) -> Int { Swift.min(Swift.max(self, low), high) }
}

/// Custom…: the system colour panel, which sets a group's colour as you pick.
@MainActor final class CustomGroupColor: NSObject {
    static let shared = CustomGroupColor()
    private var pick: ((String) -> Void)?
    private var pending: DispatchWorkItem?

    /// Opens the panel on start; pick gets each colour chosen, once you
    /// stop dragging.
    func open(start: NSColor, pick: @escaping (String) -> Void) {
        self.pick = pick
        let panel = NSColorPanel.shared
        panel.showsAlpha = false
        panel.setTarget(nil)
        panel.color = start
        panel.setTarget(self)
        panel.setAction(#selector(changed(_:)))
        panel.orderFront(nil)
    }

    @objc private func changed(_ panel: NSColorPanel) {
        let hex = GroupColor.hex(panel.color)
        pending?.cancel()
        let work = DispatchWorkItem { [weak self] in self?.pick?(hex) }
        pending = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3, execute: work)
    }
}
