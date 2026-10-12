interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  const t = rest.y ?? -1;
  console.log(t);
  return 0;
}
