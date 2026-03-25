package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PickerItem represents a selectable item in the picker.
type PickerItem struct {
	Name        string
	Description string
	Selected    bool
}

// PickerCategory groups items under a heading.
type PickerCategory struct {
	Name  string
	Items []PickerItem
}

// PickerResult holds the selected item names.
type PickerResult struct {
	Selected  []string
	Cancelled bool
}

var (
	styleCategory = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	styleCursor   = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	styleSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	styleDesc     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleHelp     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleCheck    = lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	styleUncheck  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

type pickerModel struct {
	categories []PickerCategory
	flatItems  []flatItem // flattened view of all rows
	cursor     int
	done       bool
	cancelled  bool
}

// flatItem is either a category header or a selectable item.
type flatItem struct {
	isHeader   bool
	header     string
	catIdx     int // index into categories
	itemIdx    int // index into category items
}

func newPickerModel(categories []PickerCategory) pickerModel {
	var flat []flatItem
	cursor := -1

	for ci, cat := range categories {
		flat = append(flat, flatItem{isHeader: true, header: cat.Name, catIdx: ci})
		for ii := range cat.Items {
			if cursor == -1 {
				cursor = len(flat)
			}
			flat = append(flat, flatItem{catIdx: ci, itemIdx: ii})
		}
	}

	if cursor == -1 {
		cursor = 0
	}

	return pickerModel{
		categories: categories,
		flatItems:  flat,
		cursor:     cursor,
	}
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			m.done = true
			return m, tea.Quit

		case "enter":
			m.done = true
			return m, tea.Quit

		case "up", "k":
			m.cursor = m.prevSelectable()

		case "down", "j":
			m.cursor = m.nextSelectable()

		case " ":
			fi := m.flatItems[m.cursor]
			if !fi.isHeader {
				item := &m.categories[fi.catIdx].Items[fi.itemIdx]
				item.Selected = !item.Selected
			}

		case "a":
			// Toggle all
			allSelected := m.allSelected()
			for ci := range m.categories {
				for ii := range m.categories[ci].Items {
					m.categories[ci].Items[ii].Selected = !allSelected
				}
			}
		}
	}
	return m, nil
}

func (m pickerModel) View() string {
	var b strings.Builder

	b.WriteString("\n")

	for i, fi := range m.flatItems {
		if fi.isHeader {
			b.WriteString(fmt.Sprintf("\n  %s\n", styleCategory.Render(fi.header)))
			continue
		}

		item := m.categories[fi.catIdx].Items[fi.itemIdx]
		isCursor := i == m.cursor

		check := styleUncheck.Render("[ ]")
		if item.Selected {
			check = styleCheck.Render("[x]")
		}

		name := item.Name
		if isCursor {
			name = styleCursor.Render(name)
		} else if item.Selected {
			name = styleSelected.Render(name)
		}

		desc := ""
		if item.Description != "" {
			desc = styleDesc.Render("  " + item.Description)
		}

		cursor := "  "
		if isCursor {
			cursor = styleCursor.Render("> ")
		}

		b.WriteString(fmt.Sprintf("  %s%s %-22s%s\n", cursor, check, name, desc))
	}

	b.WriteString("\n")

	count := m.selectedCount()
	summary := styleDesc.Render(fmt.Sprintf("  %d selected", count))
	b.WriteString(summary + "\n")
	b.WriteString(styleHelp.Render("  ↑/↓ navigate • space toggle • a toggle all • enter confirm • esc cancel"))
	b.WriteString("\n")

	return b.String()
}

func (m pickerModel) prevSelectable() int {
	for i := m.cursor - 1; i >= 0; i-- {
		if !m.flatItems[i].isHeader {
			return i
		}
	}
	return m.cursor
}

func (m pickerModel) nextSelectable() int {
	for i := m.cursor + 1; i < len(m.flatItems); i++ {
		if !m.flatItems[i].isHeader {
			return i
		}
	}
	return m.cursor
}

func (m pickerModel) selectedCount() int {
	count := 0
	for _, cat := range m.categories {
		for _, item := range cat.Items {
			if item.Selected {
				count++
			}
		}
	}
	return count
}

func (m pickerModel) allSelected() bool {
	for _, cat := range m.categories {
		for _, item := range cat.Items {
			if !item.Selected {
				return false
			}
		}
	}
	return true
}

// RunPicker runs the interactive multi-select picker and returns selected items.
func RunPicker(title string, categories []PickerCategory) (PickerResult, error) {
	fmt.Println()
	fmt.Println(styleLabel.Render("  " + title))
	fmt.Println(styleDim.Render("  ─────────────────────────────────"))

	m := newPickerModel(categories)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return PickerResult{Cancelled: true}, err
	}

	final := result.(pickerModel)
	if final.cancelled {
		return PickerResult{Cancelled: true}, nil
	}

	var selected []string
	for _, cat := range final.categories {
		for _, item := range cat.Items {
			if item.Selected {
				selected = append(selected, item.Name)
			}
		}
	}

	return PickerResult{Selected: selected}, nil
}
