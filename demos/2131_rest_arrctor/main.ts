interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  const a: number[] = [rest.y, rest.y + 1];
  console.log(a[0] + a[1]);
  return 0;
}
