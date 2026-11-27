package ui

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dcaceresc/pulso/internal/firewall"
	"github.com/dcaceresc/pulso/internal/netcheck"
)

// cleanReport es una sola interfaz física: no hay nada ambiguo que revisar.
func cleanReport() netcheck.Report {
	return netcheck.Report{
		LAN:        netip.MustParseAddr("192.168.1.120"),
		Candidates: []netcheck.Interface{{Name: "wlp1s0", Address: netip.MustParseAddr("192.168.1.120")}},
	}
}

// bridgeReport es el caso que la persona encontró: el cortafuegos solo informa
// que está activo, pero el puente de Docker publica una dirección virtual que
// compite con la del Wi-Fi.
func bridgeReport() netcheck.Report {
	return netcheck.Report{
		LAN: netip.MustParseAddr("192.168.1.120"),
		Candidates: []netcheck.Interface{
			{Name: "wlp1s0", Address: netip.MustParseAddr("192.168.1.120")},
			{Name: "docker0", Address: netip.MustParseAddr("172.18.0.1"), Virtual: true},
		},
		Virtual: []netcheck.Interface{{Name: "docker0", Address: netip.MustParseAddr("172.18.0.1"), Virtual: true}},
	}
}

// twoPhysicalReport tiene dos redes físicas y ninguna virtual: también compite.
func twoPhysicalReport() netcheck.Report {
	return netcheck.Report{
		LAN: netip.MustParseAddr("192.168.1.120"),
		Candidates: []netcheck.Interface{
			{Name: "wlp1s0", Address: netip.MustParseAddr("192.168.1.120")},
			{Name: "enp3s0", Address: netip.MustParseAddr("10.0.0.20")},
		},
	}
}

func activePlan() firewall.Plan {
	return firewall.Plan{
		Backend:  firewall.BackendUFW,
		Findings: []firewall.Finding{{Kind: firewall.Active, Backend: firewall.BackendUFW}},
	}
}

func planWith(kind firewall.Kind) firewall.Plan {
	plan := activePlan()
	plan.Findings = append(plan.Findings, firewall.Finding{Kind: kind, Backend: firewall.BackendUFW})
	return plan
}

// TestPairingLevelMapping fija la tabla de tres niveles. Un plan con solo
// hallazgos `Active` y un puente de Docker es ámbar: no hay nada roto, pero sí
// algo que revisar. Un `ServiceMissing` es rojo aunque la red esté limpia, y un
// diagnóstico fallido nunca es verde.
func TestPairingLevelMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report netcheck.Report
		plan   firewall.Plan
		err    error
		want   pairingLevel
	}{
		{"active firewall on one interface", cleanReport(), activePlan(), nil, pairingClear},
		{"inactive firewall on one interface", cleanReport(),
			firewall.Plan{Findings: []firewall.Finding{{Kind: firewall.Inactive}}}, nil, pairingClear},
		{"empty diagnosis on one interface", cleanReport(), firewall.Plan{}, nil, pairingClear},
		{"docker bridge with only active findings", bridgeReport(), activePlan(), nil, pairingReview},
		{"two physical addresses", twoPhysicalReport(), activePlan(), nil, pairingReview},
		{"virtual interface without a second candidate", netcheck.Report{
			LAN:        netip.MustParseAddr("192.168.1.120"),
			Candidates: []netcheck.Interface{{Name: "wlp1s0", Address: netip.MustParseAddr("192.168.1.120")}},
			Virtual:    []netcheck.Interface{{Name: "docker0", Address: netip.MustParseAddr("172.18.0.1"), Virtual: true}},
		}, activePlan(), nil, pairingReview},
		{"discovery missing on a clean network", cleanReport(), planWith(firewall.MDNSMissing), nil, pairingBlocked},
		{"service missing on a clean network", cleanReport(), planWith(firewall.ServiceMissing), nil, pairingBlocked},
		{"unreadable firewall", cleanReport(), planWith(firewall.Unverifiable), nil, pairingBlocked},
		{"service missing wins over the bridge", bridgeReport(), planWith(firewall.ServiceMissing), nil, pairingBlocked},
		{"diagnosis error", netcheck.Report{}, firewall.Plan{}, errors.New("boom"), pairingReview},
		{"diagnosis error is never green", cleanReport(), activePlan(), errors.New("boom"), pairingReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pairingLevelOf(PairingDiagnosis{Network: tc.report, Firewall: tc.plan}, tc.err)
			if got != tc.want {
				t.Fatalf("level = %d, want %d", got, tc.want)
			}
		})
	}
}

// setupLevelLine devuelve, sin códigos de color, la línea del resumen que
// empieza con el indicador esperado.
func setupLevelLine(t *testing.T, view, indicator string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		stripped := strings.TrimSpace(ansi.Strip(line))
		if strings.HasPrefix(stripped, indicator) {
			return stripped
		}
	}
	t.Fatalf("view has no summary line starting with %q:\n%s", indicator, view)
	return ""
}

// setupLevelModel entra al paso de emparejamiento con el diagnóstico dado y lo
// completa, que es lo que ve la persona.
func setupLevelModel(t *testing.T, language string, report netcheck.Report, plan firewall.Plan, err error, apply func([]string) tea.Cmd) tea.Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service := setupDiagnosisService(
		func(context.Context) (PairingDiagnosis, error) {
			return PairingDiagnosis{Network: report, Firewall: plan}, err
		}, apply)
	model := NewSetupModelWithPreferences(ctx, service, preferences{Language: language}, nil)
	return completeSetup(t, model, model.Init())
}

func setupDetailOpened(view string) bool {
	return strings.Contains(view, "Pairing diagnosis") || strings.Contains(view, "Diagnóstico del emparejamiento")
}

// TestSetupPairingLevelIndicator comprueba el indicador en los dos idiomas, que
// encabece la línea resumen y que la tecla `d` aparezca exactamente cuando hay
// algo que revisar. También fija el ancho de 80x24 con el indicador más largo.
func TestSetupPairingLevelIndicator(t *testing.T) {
	for _, tc := range []struct {
		name     string
		report   netcheck.Report
		plan     firewall.Plan
		err      error
		level    pairingLevel
		word     [2]string // inglés, español
		rest     [2]string // fragmento que sigue al indicador
		hints    [2]string // pie del paso de emparejamiento
		openable bool
	}{
		{"clear", cleanReport(), activePlan(), nil, pairingClear,
			[2]string{"OK", "OK"},
			[2]string{"LAN allowed", "LAN permitida"},
			[2]string{"Enter: recheck | b: back to the key | Esc: quit",
				"Enter: volver a comprobar | b: volver a la clave | Esc: salir"}, false},
		{"review", bridgeReport(), activePlan(), nil, pairingReview,
			[2]string{"Review", "Revisar"},
			[2]string{"1 virtual interface", "1 interfaz virtual"},
			[2]string{"b: back to the key | d: diagnostics", "b: volver a la clave | d: diagnóstico"}, true},
		{"blocked", cleanReport(), planWith(firewall.ServiceMissing), nil, pairingBlocked,
			[2]string{"Blocked", "Bloqueado"},
			[2]string{"LAN blocked", "LAN bloqueada"},
			[2]string{"b: back to the key | d: diagnostics", "b: volver a la clave | d: diagnóstico"}, true},
		{"error", netcheck.Report{}, firewall.Plan{}, errors.New("boom"), pairingReview,
			[2]string{"Review", "Revisar"},
			[2]string{"Network diagnosis unavailable", "Diagnóstico de red no disponible"},
			[2]string{"b: back to the key | d: diagnostics", "b: volver a la clave | d: diagnóstico"}, true},
	} {
		for i, language := range []string{"en", "es"} {
			t.Run(tc.name+"/"+language, func(t *testing.T) {
				model := setupLevelModel(t, language, tc.report, tc.plan, tc.err, nil)
				view := model.View()
				expectSetupBounds(t, view, 80, 24)

				line := setupLevelLine(t, view, "● "+tc.word[i])
				if !strings.Contains(line, tc.rest[i]) {
					t.Fatalf("summary line %q lost %q", line, tc.rest[i])
				}
				if !strings.Contains(view, tc.hints[i]) {
					t.Errorf("view missing hints %q:\n%s", tc.hints[i], view)
				}

				model, cmd := model.Update(setupRunes("d"))
				if cmd != nil {
					t.Fatal("opening the detail must not run a command")
				}
				if got := setupDetailOpened(model.View()); got != tc.openable {
					t.Fatalf("d opened the detail = %v, want %v:\n%s", got, tc.openable, model.View())
				}
			})
		}
	}
}

// TestSetupPairingGreenKeepsTheStepAsIs fija el caso verde: sin `d`, sin detalle
// y con las mismas pistas de antes.
func TestSetupPairingGreenKeepsTheStepAsIs(t *testing.T) {
	model := setupLevelModel(t, "en", cleanReport(), activePlan(), nil, nil)
	view := model.View()
	expectSetupText(t, view, "Waiting for pairing", "● OK",
		"Enter: recheck | b: back to the key | Esc: quit")
	if strings.Contains(view, "d: diagnostics") || strings.Contains(view, "1 virtual interface") {
		t.Fatalf("green level still offers the detail:\n%s", view)
	}
	model, cmd := model.Update(setupRunes("d"))
	if cmd != nil {
		t.Fatal("d must not run a command at green")
	}
	if setupDetailOpened(model.View()) || strings.Contains(model.View(), "a: apply rules") {
		t.Fatalf("d opened something at green:\n%s", model.View())
	}
}

// TestSetupPairingDetailSaysThereIsNothingToFix cubre el detalle sin comandos ni
// hallazgos que corregir: lo dice, no inventa una lista vacía y no ofrece `a`.
func TestSetupPairingDetailSaysThereIsNothingToFix(t *testing.T) {
	for _, tc := range []struct{ language, message, back, virtual, apply string }{
		{"en", "Nothing to fix. Pairing can still work.", "Enter: back", "Virtual interface: docker0 172.18.0.1", "a: apply rules"},
		{"es", "No hay nada que corregir. El emparejamiento puede funcionar.", "Enter: volver", "Interfaz virtual: docker0 172.18.0.1", "a: aplicar reglas"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			applied := 0
			model := setupLevelModel(t, tc.language, bridgeReport(), activePlan(), nil, func([]string) tea.Cmd {
				applied++
				return nil
			})
			model, _ = model.Update(setupRunes("d"))
			view := model.View()
			expectSetupBounds(t, view, 80, 24)
			expectSetupText(t, view, tc.message, tc.back, tc.virtual)
			if strings.Contains(view, tc.apply) {
				t.Fatalf("detail without commands offers %q:\n%s", tc.apply, view)
			}
			model, cmd := model.Update(setupRunes("a"))
			model, cmd = model.Update(setupRunes("a"))
			if cmd != nil || applied != 0 {
				t.Fatalf("a executed %d commands without a plan", applied)
			}
			if strings.Contains(model.View(), "a: confirm") {
				t.Fatalf("a armed a confirmation without a plan:\n%s", model.View())
			}
		})
	}
}

// TestSetupPairingDetailListsFindingsWithoutApply cubre un plan que no puede
// proponer comandos (firewalld, nftables): enseña los hallazgos y no ofrece `a`.
func TestSetupPairingDetailListsFindingsWithoutApply(t *testing.T) {
	applied := 0
	// Un hallazgo `Unverifiable` es rojo y no siempre trae comandos.
	model := setupLevelModel(t, "en", cleanReport(), planWith(firewall.Unverifiable), nil, func([]string) tea.Cmd {
		applied++
		return nil
	})
	view := model.View()
	expectSetupBounds(t, view, 80, 24)
	expectSetupText(t, view, "● Blocked", "d: diagnostics")

	model, _ = model.Update(setupRunes("d"))
	view = model.View()
	expectSetupText(t, view, "Firewall rules cannot be read.", "Enter: back")
	if strings.Contains(view, "a: apply rules") || strings.Contains(view, "Nothing to fix") {
		t.Fatalf("detail contradicted its findings:\n%s", view)
	}
	model, cmd := model.Update(setupRunes("a"))
	model, cmd = model.Update(setupRunes("a"))
	if cmd != nil || applied != 0 {
		t.Fatalf("a executed %d commands without commands in the plan", applied)
	}
	if strings.Contains(model.View(), "a: confirm") {
		t.Fatalf("a armed a confirmation without commands:\n%s", model.View())
	}
}
