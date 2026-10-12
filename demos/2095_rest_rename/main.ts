interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x: a, ...rest } = o;
  console.log(a + rest.y);
  return 0;
}
