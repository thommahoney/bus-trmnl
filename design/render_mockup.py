#!/usr/bin/env python3
"""Render a refined transit board mockup based on Infrastructural Silence philosophy."""

from PIL import Image, ImageDraw, ImageFont
import os

W, H = 1872, 1404
FONT_DIR = "/fonts"

def load_font(name, size):
    return ImageFont.truetype(os.path.join(FONT_DIR, name), size)

def text_width(draw, text, font):
    bbox = draw.textbbox((0, 0), text, font=font)
    return bbox[2] - bbox[0]

def text_height(draw, text, font):
    bbox = draw.textbbox((0, 0), text, font=font)
    return bbox[3] - bbox[1]

def draw_badge(draw, x, y, text, font, badge_h, badge_w, bg_color=30):
    """Draw a fixed-width rounded rectangle badge with centered white text."""
    radius = badge_h * 0.18

    draw.rounded_rectangle(
        [x, y, x + badge_w, y + badge_h],
        radius=radius,
        fill=bg_color,
    )
    cx = x + badge_w / 2
    cy = y + badge_h / 2
    draw.text((cx, cy), text, fill=255, font=font, anchor="mm")
    return badge_w


def main():
    img = Image.new("L", (W, H), 255)
    draw = ImageDraw.Draw(img)

    # --- Fonts ---
    badge_font = load_font("BigShoulders-Bold.ttf", 78)
    clock_font = load_font("IBMPlexMono-Regular.ttf", 56)
    section_font = load_font("InstrumentSans-Bold.ttf", 64)
    line_font = load_font("InstrumentSans-Bold.ttf", 70)
    dest_font = load_font("InstrumentSans-Regular.ttf", 46)
    eta_font = load_font("IBMPlexMono-Bold.ttf", 68)
    meta_font = load_font("IBMPlexMono-Regular.ttf", 28)

    margin_x = 80
    margin_top = 70
    right_edge = W - margin_x

    # ========== HEADER ==========
    y = margin_top

    # Clock — right-aligned, monospaced
    clock_text = "2:31 PM"
    cw = text_width(draw, clock_text, clock_font)
    draw.text((right_edge - cw, y), clock_text, fill=80, font=clock_font)

    # Divider
    div_y = y + 75
    draw.line([(margin_x, div_y), (right_edge, div_y)], fill=0, width=3)

    # ========== SECTION 1: 43/44 Outbound ==========
    section_y = div_y + 50

    draw.text((margin_x, section_y), "43 / 44 Outbound", fill=60, font=section_font)

    # Arrival rows
    rows_1 = [
        ("44", "O'SHAUGHNESSY", "Hudson Ave & 3rd St", "Now"),
        ("44", "O'SHAUGHNESSY", "Hudson Ave & 3rd St", "9 min"),
        ("43", "MASONIC", "Munich St & Geneva Ave", "13 min"),
    ]

    row_start_y = section_y + 95
    row_height = 120
    badge_h = 84
    badge_w = 105  # fixed width for all badges
    badge_col_x = margin_x
    line_col_x = margin_x + 175  # fixed column for line names
    dest_col_x = margin_x + 820  # fixed column for destinations (clear of O'SHAUGHNESSY)

    for i, (route, line_name, dest, eta) in enumerate(rows_1):
        ry = row_start_y + i * row_height

        # Badge
        badge_y = ry + (row_height - badge_h) / 2
        draw_badge(draw, badge_col_x, badge_y, route, badge_font, badge_h, badge_w)

        # Line name — bold, dark
        line_ty = ry + (row_height - text_height(draw, line_name, line_font)) / 2
        draw.text((line_col_x, line_ty), line_name, fill=0, font=line_font)

        # Destination — lighter gray, quieter voice
        dest_ty = ry + (row_height - text_height(draw, dest, dest_font)) / 2
        draw.text((dest_col_x, dest_ty), dest, fill=120, font=dest_font)

        # ETA — right-aligned, monospaced
        ew = text_width(draw, eta, eta_font)
        eta_ty = ry + (row_height - text_height(draw, eta, eta_font)) / 2
        draw.text((right_edge - ew, eta_ty), eta, fill=0, font=eta_font)

        # Subtle row separator (not after last row in section)
        if i < len(rows_1) - 1:
            sep_y = ry + row_height - 1
            draw.line([(line_col_x, sep_y), (right_edge, sep_y)], fill=220, width=1)

    # ========== SECTION DIVIDER ==========
    sec_div_y = row_start_y + len(rows_1) * row_height + 40
    draw.line([(margin_x, sec_div_y), (right_edge, sec_div_y)], fill=180, width=1)

    # ========== SECTION 2: N → Caltrain ==========
    section2_y = sec_div_y + 40

    draw.text((margin_x, section2_y), "N → Caltrain", fill=60, font=section_font)

    rows_2 = [
        ("N", "JUDAH", "King St & 4th St", "2 min"),
        ("N", "JUDAH", "King St & 4th St", "16 min"),
        ("N", "JUDAH", "King St & 4th St", "27 min"),
    ]

    row2_start_y = section2_y + 80

    for i, (route, line_name, dest, eta) in enumerate(rows_2):
        ry = row2_start_y + i * row_height

        badge_y = ry + (row_height - badge_h) / 2
        draw_badge(draw, badge_col_x, badge_y, route, badge_font, badge_h, badge_w)

        line_ty = ry + (row_height - text_height(draw, line_name, line_font)) / 2
        draw.text((line_col_x, line_ty), line_name, fill=0, font=line_font)

        dest_ty = ry + (row_height - text_height(draw, dest, dest_font)) / 2
        draw.text((dest_col_x, dest_ty), dest, fill=120, font=dest_font)

        ew = text_width(draw, eta, eta_font)
        eta_ty = ry + (row_height - text_height(draw, eta, eta_font)) / 2
        draw.text((right_edge - ew, eta_ty), eta, fill=0, font=eta_font)

        if i < len(rows_2) - 1:
            sep_y = ry + row_height - 1
            draw.line([(line_col_x, sep_y), (right_edge, sep_y)], fill=220, width=1)

    # ========== FOOTER ==========
    footer_y = H - margin_top - 10
    meta_text = "updated 2:31:40 PM"
    draw.text((margin_x, footer_y), meta_text, fill=160, font=meta_font)

    # Save
    img.save("/work/transit-board-mockup.png")
    print("Saved transit-board-mockup.png")


if __name__ == "__main__":
    main()
