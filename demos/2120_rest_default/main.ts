interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x = 5, ...rest } = o;
  console.log(x + rest.y);
  return 0;
}
