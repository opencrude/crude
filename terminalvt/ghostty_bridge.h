#ifndef CRUDE_GHOSTTY_BRIDGE_H
#define CRUDE_GHOSTTY_BRIDGE_H

#include <ghostty/vt.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

/* Logical cell pixel size for CSI 14/16/18 t in a cell TUI. */
#define CRUDE_CELL_PX_W 8
#define CRUDE_CELL_PX_H 16

/* cgo adds __declspec(dllexport) to //export trampolines on Windows. The
 * attribute must be present on the first declaration or MSVC/clang rejects
 * the redeclaration. On non-Windows it's a no-op. */
#ifdef _WIN32
#define CRUDE_EXPORT __declspec(dllexport)
#else
#define CRUDE_EXPORT
#endif

CRUDE_EXPORT void neutralgoWritePtyTrampoline(GhosttyTerminal, void*, uint8_t*, size_t);
CRUDE_EXPORT void neutralgoTitleChangedTrampoline(GhosttyTerminal, void*);
CRUDE_EXPORT void neutralgoBellTrampoline(GhosttyTerminal, void*);
CRUDE_EXPORT void neutralgoPwdChangedTrampoline(GhosttyTerminal, void*);
CRUDE_EXPORT GhosttyClipboardWriteResult neutralgoClipboardWriteTrampoline(GhosttyTerminal, void*, GhosttyClipboardWrite*);

/* cgo.Handle is a uintptr index, not a Go pointer. Passing it as uintptr_t
 * avoids checkptr (-race) rejecting unsafe.Pointer(handle). Ghostty userdata
 * is void*; C stores the integer bit pattern. */
static inline GhosttyResult crude_set_userdata(GhosttyTerminal t, uintptr_t ud) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_USERDATA, (void *)ud);
}

static inline GhosttyResult crude_set_write_pty(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_WRITE_PTY,
		(const void *)neutralgoWritePtyTrampoline);
}

static inline GhosttyResult crude_set_title_changed(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_TITLE_CHANGED,
		(const void *)neutralgoTitleChangedTrampoline);
}

static inline GhosttyString crude_xtversion(GhosttyTerminal t, void *ud) {
	(void)t;
	(void)ud;
	static const uint8_t ver[] = "ghostty 1.3.0";
	GhosttyString s;
	s.ptr = ver;
	s.len = sizeof(ver) - 1;
	return s;
}

static inline GhosttyResult crude_set_xtversion(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_XTVERSION,
		(const void *)crude_xtversion);
}

static inline bool crude_size_cb(GhosttyTerminal t, void *ud, GhosttySizeReportSize *out) {
	(void)ud;
	if (out == NULL) {
		return false;
	}
	uint16_t cols = 0;
	uint16_t rows = 0;
	uint32_t wp = 0;
	uint32_t hp = 0;
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_COLS, &cols);
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_ROWS, &rows);
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_WIDTH_PX, &wp);
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_HEIGHT_PX, &hp);
	out->rows = rows;
	out->columns = cols;
	out->cell_width = (cols > 0 && wp > 0) ? (wp / cols) : CRUDE_CELL_PX_W;
	out->cell_height = (rows > 0 && hp > 0) ? (hp / rows) : CRUDE_CELL_PX_H;
	return true;
}

static inline GhosttyResult crude_set_size(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_SIZE,
		(const void *)crude_size_cb);
}

static inline bool crude_da_cb(GhosttyTerminal t, void *ud, GhosttyDeviceAttributes *out) {
	(void)t;
	(void)ud;
	if (out == NULL) {
		return false;
	}
	memset(out, 0, sizeof(*out));
	out->primary.conformance_level = GHOSTTY_DA_CONFORMANCE_VT220;
	out->primary.features[0] = GHOSTTY_DA_FEATURE_COLUMNS_132;
	out->primary.features[1] = GHOSTTY_DA_FEATURE_SELECTIVE_ERASE;
	out->primary.features[2] = GHOSTTY_DA_FEATURE_ANSI_COLOR;
	out->primary.num_features = 3;
	out->secondary.device_type = GHOSTTY_DA_DEVICE_TYPE_VT220;
	out->secondary.firmware_version = 0;
	out->secondary.rom_cartridge = 0;
	out->tertiary.unit_id = 0;
	return true;
}

static inline GhosttyResult crude_set_device_attributes(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_DEVICE_ATTRIBUTES,
		(const void *)crude_da_cb);
}

static inline GhosttyResult crude_set_terminfo_name(GhosttyTerminal t) {
	static const uint8_t name[] = "xterm-ghostty";
	GhosttyString s;
	s.ptr = name;
	s.len = sizeof(name) - 1;
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_TERMINFO_NAME, &s);
}

static inline GhosttyResult crude_set_scrollback_lines(GhosttyTerminal t, size_t lines) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_LINES, &lines);
}

static inline GhosttyResult crude_set_scrollback_bytes(GhosttyTerminal t, size_t bytes) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &bytes);
}

static inline GhosttyResult crude_set_default_cursor_blink(GhosttyTerminal t, bool blink) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_DEFAULT_CURSOR_BLINK, &blink);
}

static inline void crude_scroll_delta(GhosttyTerminal t, intptr_t delta) {
	GhosttyTerminalScrollViewport vp;
	memset(&vp, 0, sizeof(vp));
	vp.tag = GHOSTTY_SCROLL_VIEWPORT_DELTA;
	vp.value.delta = delta;
	ghostty_terminal_scroll_viewport(t, vp);
}

static inline void crude_scroll_top(GhosttyTerminal t) {
	GhosttyTerminalScrollViewport vp;
	memset(&vp, 0, sizeof(vp));
	vp.tag = GHOSTTY_SCROLL_VIEWPORT_TOP;
	ghostty_terminal_scroll_viewport(t, vp);
}

static inline void crude_scroll_bottom(GhosttyTerminal t) {
	GhosttyTerminalScrollViewport vp;
	memset(&vp, 0, sizeof(vp));
	vp.tag = GHOSTTY_SCROLL_VIEWPORT_BOTTOM;
	ghostty_terminal_scroll_viewport(t, vp);
}

typedef struct {
	bool has_styling;
	bool has_foreground;
	GhosttyColorRgb foreground;
	bool has_background;
	GhosttyColorRgb background;
	bool bold;
	bool faint;
	bool italic;
	bool underline;
	bool strikethrough;
	bool inverse;
} crude_cell_style;

typedef struct {
	GhosttyResult result;
	crude_cell_style style;
} crude_cell_style_result;

static inline crude_cell_style_result crude_cell_style_get(GhosttyRenderStateRowCells cells) {
	crude_cell_style_result out;
	memset(&out, 0, sizeof(out));
	out.result = GHOSTTY_SUCCESS;

	GhosttyResult result = ghostty_render_state_row_cells_get(
		cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_HAS_STYLING, &out.style.has_styling);
	if (result != GHOSTTY_SUCCESS) {
		out.result = result;
		return out;
	}
	if (out.style.has_styling) {
		GhosttyStyle style = GHOSTTY_INIT_SIZED(GhosttyStyle);
		result = ghostty_render_state_row_cells_get(
			cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style);
		if (result != GHOSTTY_SUCCESS) {
			out.result = result;
			return out;
		}
		out.style.bold = style.bold;
		out.style.faint = style.faint;
		out.style.italic = style.italic;
		out.style.underline = style.underline != GHOSTTY_SGR_UNDERLINE_NONE;
		out.style.strikethrough = style.strikethrough;
		out.style.inverse = style.inverse;
	}
	result = ghostty_render_state_row_cells_get(
		cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &out.style.foreground);
	if (result == GHOSTTY_SUCCESS) {
		out.style.has_foreground = true;
	} else if (result != GHOSTTY_INVALID_VALUE) {
		out.result = result;
		return out;
	}
	result = ghostty_render_state_row_cells_get(
		cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &out.style.background);
	if (result == GHOSTTY_SUCCESS) {
		out.style.has_background = true;
	} else if (result != GHOSTTY_INVALID_VALUE) {
		out.result = result;
	}
	return out;
}

typedef struct {
	GhosttyResult result;
	size_t len;
} crude_utf8_result;

static inline crude_utf8_result crude_cell_utf8(GhosttyRenderStateRowCells cells, uint8_t *dst, size_t dst_len) {
	GhosttyBuffer buf;
	buf.ptr = dst;
	buf.cap = dst_len;
	buf.len = 0;
	crude_utf8_result out;
	out.result = ghostty_render_state_row_cells_get(
		cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_UTF8, &buf);
	out.len = buf.len;
	return out;
}

static inline GhosttyResult crude_set_default_fg(GhosttyTerminal t, uint8_t r, uint8_t g, uint8_t b) {
	GhosttyColorRgb c;
	c.r = r;
	c.g = g;
	c.b = b;
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, &c);
}

static inline GhosttyResult crude_set_default_bg(GhosttyTerminal t, uint8_t r, uint8_t g, uint8_t b) {
	GhosttyColorRgb c;
	c.r = r;
	c.g = g;
	c.b = b;
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, &c);
}

static inline uint8_t crude_kitty_flags(GhosttyTerminal t) {
	uint8_t flags = 0;
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_KITTY_KEYBOARD_FLAGS, &flags);
	return flags;
}

static inline GhosttyResult crude_set_bell(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_BELL,
		(const void *)neutralgoBellTrampoline);
}

static inline GhosttyResult crude_set_pwd_changed(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_PWD_CHANGED,
		(const void *)neutralgoPwdChangedTrampoline);
}

static inline GhosttyResult crude_set_clipboard_write(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE,
		(const void *)neutralgoClipboardWriteTrampoline);
}

static inline bool crude_viewport_pinned(GhosttyTerminal t) {
	bool active = true;
	if (ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_VIEWPORT_ACTIVE, &active) != GHOSTTY_SUCCESS) {
		return true;
	}
	return active;
}

static inline bool crude_active_screen_alt(GhosttyTerminal t) {
	GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
	if (ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen) != GHOSTTY_SUCCESS) {
		return false;
	}
	return screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE;
}

static inline bool crude_mode_value(GhosttyTerminal t, GhosttyMode mode) {
	GhosttyTerminalModeConfig cfg;
	cfg.mode = mode;
	cfg.value = false;
	if (ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_MODE, &cfg) != GHOSTTY_SUCCESS) {
		return false;
	}
	return cfg.value;
}

/* Stop at mode transitions, not byte-pattern matches. Ghostty alone owns VT
 * parsing, including continuations, cancelled CSI and OSC payloads. ASCII text
 * runs stay bulk writes; non-ground sequences use its shortest-prefix API. */
static inline size_t crude_write_visual_boundary(GhosttyTerminal t, const uint8_t *data, size_t len) {
	const bool before = crude_mode_value(t, GHOSTTY_MODE_SYNC_OUTPUT);
	size_t at = 0;
	while (at < len) {
		bool ground = false;
		ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_VT_GROUND, &ground);
		if (ground) {
			size_t end = at;
			while (end < len && data[end] >= 0x20 && data[end] < 0x7f) end++;
			if (end > at) {
				ghostty_terminal_vt_write(t, data + at, end - at);
				at = end;
				continue;
			}
			ghostty_terminal_vt_write(t, data + at, 1);
			at++;
		}
		size_t consumed = 0;
		ghostty_terminal_vt_write_until_ground(t, data + at, len - at, &consumed);
		at += consumed;
		if (crude_mode_value(t, GHOSTTY_MODE_SYNC_OUTPUT) != before) return at;
	}
	return at;
}

static inline void crude_end_sync_output(GhosttyTerminal t) {
	GhosttyTerminalModeConfig cfg;
	cfg.mode = GHOSTTY_MODE_SYNC_OUTPUT;
	cfg.value = false;
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_MODE, &cfg);
}

#endif /* CRUDE_GHOSTTY_BRIDGE_H */
