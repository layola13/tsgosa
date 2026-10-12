interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  console.log(JSON.stringify(rest));
  return 0;
}
