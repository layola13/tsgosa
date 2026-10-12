interface P { x: number; y: number; }
function pick(o: P, k: boolean): i32 {
  const { x, ...rest } = o;
  if (k) { return rest.y; }
  return x;
}
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  console.log(pick(o, true) + pick(o, false));
  return 0;
}
