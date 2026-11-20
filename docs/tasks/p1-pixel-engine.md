# p1/pixel-engine: animated pixel art
Owner: **Vinayak** · Base: `p1/ui-shell` · Needs: p1/ui-shell

## Goal
A tiny canvas engine and the scenes: starfield, radar with peer ships, the cargo drone that shows transfer progress, and the SAS glyphs. All art original.

## Files
`web/js/pixel/` (new), `web/css/` (scene containers), `web/tests/`.

## Design
- `loop.js`: one `requestAnimationFrame` loop for all scenes, capped at 30 fps; stops when the window is hidden or after 60 s idle (PrivMeet `stillness.js` idea), stops entirely for `prefers-reduced-motion` (scenes draw one still frame).
- `canvas.js`: art pixels scaled by an integer, `imageSmoothingEnabled = false`, device-pixel-ratio aware.
- Static layers (far stars, nebula, radar grid) drawn **once** to an offscreen canvas and redrawn only on resize or theme change (PrivMeet `PixelScape` idea, Bayer dither).
- `sprites.js`: palette-indexed sprites as strings (like `tools/genicon`), recolored from CSS variables.
- 16 ship avatars from 4 hull templates × 4 palettes; ship position on the radar from a hash of the peer ID.
- `glyphs.js`: 64 glyphs, 5×5 mirror-symmetric, generated from the index; all 64 distinct.

## Commits
1. `feat(pixel): add frame loop, crisp canvas and idle pause`
2. `feat(pixel): add palette sprites, ships and SAS glyphs`
3. `feat(pixel): add starfield and radar scenes`
4. `feat(pixel): add cargo drone progress and docking bay effects`
5. `test(pixel): ...`: sprite decode, 64 distinct glyphs, fps cap and idle pause with a fake clock, ship placement determinism.

## Done when
- Idle window: under 2 % of one CPU core after the 60 s idle pause (Task Manager).
- Reduced motion: nothing moves, everything still readable.
- `task check` passes.
