//go:build !linux

package main

import "os"

func termSize() (int, int) { return 0, 0 }

func isTerminal(*os.File) bool { return false }

type termState struct{}

func rawMode(*os.File, uint8, uint8) (*termState, bool) { return nil, false }

func (s *termState) restore() {}

func queryTerminalPalette() map[string]string { return nil }
