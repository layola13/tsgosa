interface P { x: number; y: number; }
function main(): i32 {
  let o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  rest = { y: 9 };
  console.log(rest.y);
  return 0;
}
