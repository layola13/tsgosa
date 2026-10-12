interface P { x: number; y: number; }
function get(o: P): i32 {
  const { x, ...rest } = o;
  return x + rest.y;
}
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  console.log(get(o));
  return 0;
}
