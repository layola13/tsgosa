interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  if ("y" in rest) { console.log(1); } else { console.log(0); }
  if ("x" in rest) { console.log(1); } else { console.log(0); }
  return 0;
}
