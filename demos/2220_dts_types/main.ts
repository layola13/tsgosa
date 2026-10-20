import { Point, Kind, Label } from "./types";

export function main(): i32 {
  const p: Point = { x: 3, y: 4 };
  const k: Kind = Kind.B;
  const l: Label = "hi";
  console.log(p.x);
  console.log(k);
  console.log(l.length);
  return 0;
}
