interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  try { console.log(rest.y); } catch (e) { console.log(0); }
  return 0;
}
