interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  let i = 0;
  do { i = i + rest.y; } while (i < 4);
  console.log(i);
  return 0;
}
