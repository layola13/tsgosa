interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  const s = "v=" + rest.y;
  console.log(s);
  return 0;
}
