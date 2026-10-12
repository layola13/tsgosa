interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { ...rest, x } = o;
  console.log(x);
  return 0;
}
