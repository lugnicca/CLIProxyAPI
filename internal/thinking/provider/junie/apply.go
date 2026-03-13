// Package junie implements the thinking ProviderApplier for the JetBrains Junie/Grazie provider.
// JetBrains Grazie does not expose thinking controls, so this applier is a no-op pass-through.
package junie

import (
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
)

// Applier implements thinking.ProviderApplier for Junie (JetBrains Grazie) models.
type Applier struct{}

var _ thinking.ProviderApplier = (*Applier)(nil)

// NewApplier creates a new Junie thinking applier.
func NewApplier() *Applier { return &Applier{} }

func init() {
	thinking.RegisterProvider("junie", NewApplier())
}

// Apply returns the request body unchanged because the Grazie API does not expose
// thinking / reasoning controls.
func (a *Applier) Apply(body []byte, _ thinking.ThinkingConfig, _ *registry.ModelInfo) ([]byte, error) {
	// Grazie API does not expose thinking controls; return body unchanged.
	return body, nil
}
