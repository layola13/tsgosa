interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  let i = 0;
  while (i < 2) { i++; }
  console.log(i + rest.y);
  return 0;
}
