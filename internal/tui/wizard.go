package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleFocus  = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	styleLabel  = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleDone   = lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	styleError  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	stylePrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
)

// WizardField defines a single prompt in the wizard.
type WizardField struct {
	Label       string
	Placeholder string
	Default     string
	Validate    func(string) error // optional
}

// WizardResult holds the collected answers, keyed by field index.
type WizardResult struct {
	Values    []string
	Cancelled bool
}

type wizardModel struct {
	fields  []WizardField
	inputs  []textinput.Model
	current int
	errors  []string
	done    bool
}

func newWizardModel(fields []WizardField) wizardModel {
	inputs := make([]textinput.Model, len(fields))
	errors := make([]string, len(fields))

	for i, f := range fields {
		t := textinput.New()
		t.Placeholder = f.Placeholder
		if f.Default != "" {
			t.SetValue(f.Default)
		}
		t.PromptStyle = stylePrompt
		t.TextStyle = styleFocus
		if i == 0 {
			t.Focus()
		}
		inputs[i] = t
	}

	return wizardModel{
		fields:  fields,
		inputs:  inputs,
		errors:  errors,
		current: 0,
	}
}

func (m wizardModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.done = true
			// Mark as cancelled by clearing inputs
			for i := range m.inputs {
				m.inputs[i].SetValue("")
			}
			m.current = -1 // sentinel for cancelled
			return m, tea.Quit

		case tea.KeyEnter:
			val := strings.TrimSpace(m.inputs[m.current].Value())
			// Use default if blank
			if val == "" && m.fields[m.current].Default != "" {
				val = m.fields[m.current].Default
				m.inputs[m.current].SetValue(val)
			}
			// Validate
			if m.fields[m.current].Validate != nil {
				if err := m.fields[m.current].Validate(val); err != nil {
					m.errors[m.current] = err.Error()
					return m, nil
				}
			}
			if val == "" {
				m.errors[m.current] = "required"
				return m, nil
			}
			m.errors[m.current] = ""

			if m.current == len(m.inputs)-1 {
				m.done = true
				return m, tea.Quit
			}
			m.inputs[m.current].Blur()
			m.current++
			m.inputs[m.current].Focus()
			return m, textinput.Blink
		}
	}

	var cmd tea.Cmd
	m.inputs[m.current], cmd = m.inputs[m.current].Update(msg)
	return m, cmd
}

func (m wizardModel) View() string {
	var b strings.Builder
	b.WriteString("\n")

	for i, field := range m.fields {
		val := m.inputs[i].Value()

		if i < m.current {
			// Completed field — show summary line
			b.WriteString(fmt.Sprintf("  %s %s\n",
				styleDone.Render("✓ "+field.Label+":"),
				styleDim.Render(val),
			))
		} else if i == m.current {
			// Active field
			b.WriteString(fmt.Sprintf("  %s\n", styleLabel.Render(field.Label)))
			b.WriteString(fmt.Sprintf("  %s\n", m.inputs[i].View()))
			if m.errors[i] != "" {
				b.WriteString(fmt.Sprintf("  %s\n", styleError.Render("✗ "+m.errors[i])))
			}
			b.WriteString("\n")
			b.WriteString(styleDim.Render("  enter to confirm • esc to cancel"))
			b.WriteString("\n")
		}
		// Future fields are hidden until reached
	}
	return b.String()
}

// RunWizard runs the interactive wizard and returns collected values.
func RunWizard(title string, fields []WizardField) (WizardResult, error) {
	fmt.Println()
	fmt.Println(styleLabel.Render("  " + title))
	fmt.Println(styleDim.Render("  ─────────────────────────────────"))

	m := newWizardModel(fields)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return WizardResult{Cancelled: true}, err
	}

	final := result.(wizardModel)
	if final.current == -1 {
		return WizardResult{Cancelled: true}, nil
	}

	values := make([]string, len(fields))
	for i, inp := range final.inputs {
		v := strings.TrimSpace(inp.Value())
		if v == "" {
			v = fields[i].Default
		}
		values[i] = v
	}
	return WizardResult{Values: values}, nil
}
