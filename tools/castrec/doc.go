// Command castrec records a console program as an asciicast v2 file. It runs
// the program in a Windows pseudo console (ConPTY), drives it from a step
// script and stores the output with timed marks, so a demo can be edited and
// rendered again later without a new recording. scripts/demo-video plays the
// casts back.
//
//	go run ./tools/castrec -cols 160 -rows 45 -cwd DIR -out demo.cast -script steps.txt -- pwsh -NoLogo
//
// Steps, one per line:
//
//	sleep:SECONDS       wait
//	mark:NAME           write a named marker event
//	key:SPEC            press a key such as Return, shift+Tab or ctrl+alt+y
//	type:TEXT           type text quickly
//	typeslow:MS:TEXT    type text with MS milliseconds between characters
//	typeenv:NAME        type the value of an environment variable
//	click:COL,ROW       left click (zero-based cell coordinates)
//	rclick:COL,ROW      right click
//	move:COL,ROW        move the pointer
//	drag:C0,R0,C1,R1    drag with the left button
//	wheel:COL,ROW,N     scroll N steps; a negative N scrolls up
//	waitfile:PATH       wait until PATH exists; meanwhile run steps written to OUT.inject
//	quit                stop running steps
//
// The cast holds only the program's output, so typed secrets appear only if
// the program echoes them. Pointer steps are also written as "i" events so a
// player can draw the pointer. Keys go in as win32-input-mode records, which
// conhost turns into exact key events with modifiers. castrec runs only on
// Windows.
package main
