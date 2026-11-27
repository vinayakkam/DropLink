// Palette-indexed sprites written as strings: one character per art
// pixel, "." transparent, other letters are palette keys (palette.js) or
// role letters remapped per sprite (h hull, s shade, t thruster). All art
// here is original to DropLink.

/**
 * @typedef {{ w: number, h: number, rows: string[] }} Sprite
 * @param {string[]} rows
 * @returns {Sprite}
 */
export function parseSprite(rows) {
  if (!rows.length) throw new Error("sprite: no rows");
  const w = rows[0].length;
  for (const r of rows) if (r.length !== w) throw new Error(`sprite: row "${r}" is not ${w} wide`);
  return { w, h: rows.length, rows };
}

/**
 * Draws a sprite at art pixel (x, y). Runs of one color in a row become a
 * single fillRect.
 * @param {CanvasRenderingContext2D} ctx
 * @param {Sprite} s
 * @param {number} x @param {number} y
 * @param {Record<string, string>} pal palette (letter → color)
 * @param {Record<string, string>} [roles] role letter → palette letter
 */
export function drawSprite(ctx, s, x, y, pal, roles = {}) {
  x = Math.round(x);
  y = Math.round(y);
  for (let row = 0; row < s.h; row++) {
    const line = s.rows[row];
    let col = 0;
    while (col < s.w) {
      const ch = line[col];
      let run = 1;
      while (col + run < s.w && line[col + run] === ch) run++;
      if (ch !== ".") {
        const color = pal[roles[ch] ?? ch];
        if (color) {
          ctx.fillStyle = color;
          ctx.fillRect(x + col, y + row, run, 1);
        }
      }
      col += run;
    }
  }
}

// Ship hulls, seen from above, nose up. 9 x 9.
export const HULLS = [
  // Dart
  ["....h....", "...hwh...", "...hhh...", "..shhhs..", ".sshhhss.", "sshh.hhss", "s.h...h.s", "..t...t..", "........."],
  // Wing
  ["....h....", "...hhh...", "s..hwh..s", "ss.hhh.ss", "sshhhhhss", ".shhhhhs.", "..h.t.h..", "....t....", "........."],
  // Disc
  ["...hhh...", "..hhwhh..", ".hhwwwhh.", "shhhhhhhs", "sshhhhhss", ".s.s.s.s.", "..t.t.t..", ".........", "........."],
  // Hauler
  ["..hhhhh..", "..hwhwh..", ".shhhhhs.", ".shh.hhs.", ".shhhhhs.", ".shh.hhs.", "..hhhhh..", "..t...t..", "........."],
].map(parseSprite);

// Hull color schemes: [hull, shade].
export const LIVERIES = [
  ["c", "C"],
  ["m", "M"],
  ["a", "A"],
  ["l", "d"],
];

/**
 * The ship for avatar 0..15: hull = avatar & 3, livery = avatar >> 2.
 * @param {number} avatar
 */
export function ship(avatar) {
  const a = ((avatar | 0) & 15) >>> 0;
  const [h, s] = LIVERIES[a >> 2];
  return { sprite: HULLS[a & 3], roles: { h, s, t: "a" } };
}

// The cargo drone, side view, two frames (thrusters flicker). 13 x 8.
export const DRONE = [
  ["....ccccc....", "..ccCwwwCcc..", ".ccCCCCCCCcc.", "cCCdddddddCCc", ".C.e.....e.C.", ".C.........C.", ".t.........t.", "............."],
  ["....ccccc....", "..ccCwwwCcc..", ".ccCCCCCCCcc.", "cCCdddddddCCc", ".C.e.....e.C.", ".C.........C.", ".T.........T.", ".t.........t."],
].map(parseSprite);

export const DRONE_ROLES = { t: "a", T: "A" };

// A data cube the drone carries. 4 x 4.
export const CUBE = parseSprite(["acca", "cwcc", "cccC", "aCCa"]);
