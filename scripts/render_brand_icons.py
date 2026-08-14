from pathlib import Path

from PIL import Image, ImageDraw


ROOT = Path(__file__).resolve().parents[1]
WEB = ROOT / "web"


def render(size: int) -> Image.Image:
    scale = 4
    canvas_size = size * scale
    unit = canvas_size / 64
    image = Image.new("RGBA", (canvas_size, canvas_size), (0, 0, 0, 0))
    draw = ImageDraw.Draw(image)

    draw.rounded_rectangle(
        (0, 0, canvas_size - 1, canvas_size - 1),
        radius=round(18 * unit),
        fill="#102C2B",
    )

    main_points = [(17 * unit, 39.5 * unit), (26 * unit, 30 * unit), (34 * unit, 38 * unit), (47 * unit, 23 * unit)]
    main_width = round(5 * unit)
    draw.line(main_points, fill="#53D6B1", width=main_width, joint="curve")
    for x, y in main_points:
        radius = main_width / 2
        draw.ellipse((x - radius, y - radius, x + radius, y + radius), fill="#53D6B1")

    arrow_points = [(42 * unit, 23 * unit), (47 * unit, 23 * unit), (47 * unit, 28 * unit)]
    arrow_width = round(4 * unit)
    draw.line(arrow_points, fill="#E9B35E", width=arrow_width, joint="curve")
    for x, y in arrow_points:
        radius = arrow_width / 2
        draw.ellipse((x - radius, y - radius, x + radius, y + radius), fill="#E9B35E")

    for x, y, radius, color in (
        (17, 40, 4, "#E9B35E"),
        (34, 38, 4, "#53D6B1"),
    ):
        draw.ellipse(
            ((x - radius) * unit, (y - radius) * unit, (x + radius) * unit, (y + radius) * unit),
            fill=color,
        )

    return image.resize((size, size), Image.Resampling.LANCZOS)


def main() -> None:
    render(192).save(WEB / "favicon-192.png", optimize=True)
    render(512).save(WEB / "favicon-512.png", optimize=True)
    render(180).save(WEB / "apple-touch-icon.png", optimize=True)
    render(256).save(WEB / "favicon.ico", sizes=[(16, 16), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])


if __name__ == "__main__":
    main()
