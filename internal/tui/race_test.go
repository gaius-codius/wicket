//go:build race

package tui

// raceEnabled is true under -race, where a render costs about ten times as
// much and the full size sweeps run past the default test timeout.
const raceEnabled = true
