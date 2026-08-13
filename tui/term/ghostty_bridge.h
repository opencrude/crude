#ifndef CRUDE_GHOSTTY_BRIDGE_H
#define CRUDE_GHOSTTY_BRIDGE_H

#include <ghostty/vt.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

/* Logical cell pixel size for CSI 14/16/18 t in a cell TUI. */
#define CRUDE_CELL_PX_W 8
#define CRUDE_CELL_PX_H 16

extern void goWritePtyTrampoline(GhosttyTerminal, void*, uint8_t*, size_t);
extern void goTitleChangedTrampoline(GhosttyTerminal, void*);
extern void goBellTrampoline(GhosttyTerminal, void*);
extern void goPwdChangedTrampoline(GhosttyTerminal, void*);
extern GhosttyClipboardWriteResult goClipboardWriteTrampoline(GhosttyTerminal, void*, GhosttyClipboardWrite*);

/* cgo.Handle is a uintptr index, not a Go pointer. Passing it as uintptr_t
 * avoids checkptr (-race) rejecting unsafe.Pointer(handle). Ghostty userdata
 * is void*; C stores the integer bit pattern. */
static inline GhosttyResult crude_set_userdata(GhosttyTerminal t, uintptr_t ud) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_USERDATA, (void *)ud);
}

static inline GhosttyResult crude_set_write_pty(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_WRITE_PTY,
		(const void *)goWritePtyTrampoline);
}

static inline GhosttyResult crude_set_title_changed(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_TITLE_CHANGED,
		(const void *)goTitleChangedTrampoline);
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
		(const void *)goBellTrampoline);
}

static inline GhosttyResult crude_set_pwd_changed(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_PWD_CHANGED,
		(const void *)goPwdChangedTrampoline);
}

static inline GhosttyResult crude_set_clipboard_write(GhosttyTerminal t) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE,
		(const void *)goClipboardWriteTrampoline);
}

static inline bool crude_viewport_pinned(GhosttyTerminal t) {
	bool active = true;
	if (ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_VIEWPORT_ACTIVE, &active) != GHOSTTY_SUCCESS) {
		return true;
	}
	return active;
}

/* --- selection --- */

static inline GhosttyResult crude_grid_ref_at_viewport(GhosttyTerminal t, uint16_t x, uint32_t y, GhosttyGridRef *out) {
	GhosttyPoint p;
	memset(&p, 0, sizeof(p));
	p.tag = GHOSTTY_POINT_TAG_VIEWPORT;
	p.value.coordinate.x = x;
	p.value.coordinate.y = y;
	*out = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	return ghostty_terminal_grid_ref(t, p, out);
}

/* Sets REF, POSITION, TIME_NS (when nonzero) and, for drags, GEOMETRY on a
 * reusable gesture event. Positions are synthetic surface pixels derived from
 * the logical cell size; x_frac picks where inside the cell the pointer sits
 * (the gesture includes a cell in the selection based on a 60%-of-cell
 * threshold, so cell-grid callers steer inclusion with the fraction). Timed
 * presses also configure multi-click repeat parameters. */
static inline GhosttyResult crude_gesture_event_prepare(
		GhosttySelectionGestureEvent ev, GhosttyTerminal t,
		uint16_t cx, uint32_t cy, double x_frac, uint64_t time_ns,
		uint32_t cols, uint32_t rows, bool with_geometry) {
	GhosttyGridRef ref;
	GhosttyResult rc = crude_grid_ref_at_viewport(t, cx, cy, &ref);
	if (rc != GHOSTTY_SUCCESS) {
		return rc;
	}
	rc = ghostty_selection_gesture_event_set(ev, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_REF, &ref);
	if (rc != GHOSTTY_SUCCESS) {
		return rc;
	}
	GhosttySurfacePosition pos;
	pos.x = ((double)cx + x_frac) * CRUDE_CELL_PX_W;
	pos.y = (double)cy * CRUDE_CELL_PX_H + CRUDE_CELL_PX_H / 2.0;
	rc = ghostty_selection_gesture_event_set(ev, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_POSITION, &pos);
	if (rc != GHOSTTY_SUCCESS) {
		return rc;
	}
	if (time_ns != 0) {
		rc = ghostty_selection_gesture_event_set(ev, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_TIME_NS, &time_ns);
		if (rc != GHOSTTY_SUCCESS) {
			return rc;
		}
		uint64_t repeat_ns = 500000000ULL;
		rc = ghostty_selection_gesture_event_set(ev, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_REPEAT_INTERVAL_NS, &repeat_ns);
		if (rc != GHOSTTY_SUCCESS) {
			return rc;
		}
		double repeat_dist = CRUDE_CELL_PX_W * 1.5;
		rc = ghostty_selection_gesture_event_set(ev, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_REPEAT_DISTANCE, &repeat_dist);
		if (rc != GHOSTTY_SUCCESS) {
			return rc;
		}
	}
	if (with_geometry) {
		GhosttySelectionGestureGeometry g;
		g.columns = cols;
		g.cell_width = CRUDE_CELL_PX_W;
		g.padding_left = 0;
		g.screen_height = rows * CRUDE_CELL_PX_H;
		rc = ghostty_selection_gesture_event_set(ev, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_GEOMETRY, &g);
		if (rc != GHOSTTY_SUCCESS) {
			return rc;
		}
	}
	return GHOSTTY_SUCCESS;
}

static inline bool crude_active_screen_alt(GhosttyTerminal t) {
	GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
	if (ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen) != GHOSTTY_SUCCESS) {
		return false;
	}
	return screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE;
}

typedef struct {
	GhosttyResult result;
	bool has;
	GhosttySelection sel;
} crude_gesture_result;

static inline crude_gesture_result crude_gesture_apply(
		GhosttySelectionGesture g, GhosttyTerminal t, GhosttySelectionGestureEvent ev) {
	crude_gesture_result out;
	memset(&out, 0, sizeof(out));
	out.sel.size = sizeof(GhosttySelection);
	GhosttyResult rc = ghostty_selection_gesture_event(g, t, ev, &out.sel);
	if (rc == GHOSTTY_SUCCESS) {
		out.result = GHOSTTY_SUCCESS;
		out.has = true;
	} else if (rc == GHOSTTY_NO_VALUE) {
		out.result = GHOSTTY_SUCCESS;
		out.has = false;
	} else {
		out.result = rc;
	}
	return out;
}

static inline GhosttyResult crude_install_selection(GhosttyTerminal t, const GhosttySelection *sel) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_SELECTION, sel);
}

static inline bool crude_has_selection(GhosttyTerminal t) {
	GhosttySelection sel;
	memset(&sel, 0, sizeof(sel));
	sel.size = sizeof(GhosttySelection);
	return ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_SELECTION, &sel) == GHOSTTY_SUCCESS;
}

static inline GhosttyResult crude_select_all(GhosttyTerminal t) {
	GhosttySelection sel;
	memset(&sel, 0, sizeof(sel));
	sel.size = sizeof(GhosttySelection);
	GhosttyResult rc = ghostty_terminal_select_all(t, &sel);
	if (rc != GHOSTTY_SUCCESS) {
		return rc;
	}
	return crude_install_selection(t, &sel);
}

typedef struct {
	GhosttyResult result;
	uint8_t *ptr;
	size_t len;
} crude_selection_text_result;

/* Formats the active selection as plain text with Ghostty's clipboard
 * semantics (unwrap soft wraps, trim trailing whitespace). */
static inline crude_selection_text_result crude_selection_text(GhosttyTerminal t) {
	crude_selection_text_result out;
	memset(&out, 0, sizeof(out));
	GhosttyTerminalSelectionFormatOptions opts;
	memset(&opts, 0, sizeof(opts));
	opts.size = sizeof(GhosttyTerminalSelectionFormatOptions);
	opts.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN;
	opts.unwrap = true;
	opts.trim = true;
	opts.selection = NULL;
	out.result = ghostty_terminal_selection_format_alloc(t, NULL, opts, &out.ptr, &out.len);
	return out;
}

static inline void crude_free(void *ptr, size_t len) {
	ghostty_free(NULL, ptr, len);
}

typedef struct {
	bool has;
	uint16_t start_x;
	uint16_t end_x;
} crude_row_selection_result;

static inline crude_row_selection_result crude_row_selection(GhosttyRenderStateRowIterator it) {
	crude_row_selection_result out;
	out.has = false;
	out.start_x = 0;
	out.end_x = 0;
	GhosttyRenderStateRowSelection sel;
	memset(&sel, 0, sizeof(sel));
	sel.size = sizeof(GhosttyRenderStateRowSelection);
	if (ghostty_render_state_row_get(it, GHOSTTY_RENDER_STATE_ROW_DATA_SELECTION, &sel) == GHOSTTY_SUCCESS) {
		out.has = true;
		out.start_x = sel.start_x;
		out.end_x = sel.end_x;
	}
	return out;
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

#endif /* CRUDE_GHOSTTY_BRIDGE_H */
