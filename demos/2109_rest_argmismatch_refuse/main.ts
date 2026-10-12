interface P { x: number; y: number; }
function sum(o: P): i32 {
  return o.x + o.y;
}
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  console.log(sum(rest));
  return 0;
}
