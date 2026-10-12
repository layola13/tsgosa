interface P { x: number; y: number; }
function f({ x, ...rest }: P): i32 {
  return x + rest.y;
}
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  console.log(f(o));
  return 0;
}
