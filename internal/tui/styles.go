package tui

import "github.com/charmbracelet/lipgloss"

// ANSI palette per the README styling table. Plain numbered colors so the
// user's terminal theme decides the actual shades.
var (
	styHeader    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true) // cyan
	styColHead   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styDotYou    = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true) // bold red ●
	styDotThem   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))            // dim ○
	stySinceNew  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // < 24h
	stySinceMid  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))            // 1–3d
	stySinceOld  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))            // > 3d
	styRoleAuth  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))            // dim grey: my own PRs
	styRoleRev   = lipgloss.NewStyle().Bold(true)                                 // bright: I'm on the hook
	stySelected  = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true).Reverse(true)
	styDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styOK        = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styFilterOn  = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	styMark      = lipgloss.NewStyle().Foreground(lipgloss.Color("4")) // ⎇ / ⌗ markers
	styForce     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styDetailKey = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)
