interface P { x: number; y: number; }
function id(o: P): P { return o; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = id(o);
  console.log(x + rest.y);
  return 0;
}
