interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { ...rest } = o;
  console.log(rest.x + rest.y);
  return 0;
}
