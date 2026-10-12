interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  const v = rest.y > 1 ? rest.y : 0;
  console.log(v);
  return 0;
}
