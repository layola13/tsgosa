interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  for (const v of rest) { console.log(v); }
  return 0;
}
