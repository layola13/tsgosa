import { Box, E } from "./lib/index";
export function main(): i32 {
  const b: Box = { v: 1 };
  const r = b.v + E.A;
  console.log(r);
  return 0;
}
