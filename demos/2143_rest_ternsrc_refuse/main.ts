interface P { x: number; y: number; }
function main(): i32 {
  const o1: P = { x: 1, y: 2 };
  const o2: P = { x: 10, y: 20 };
  const { x, ...rest } = true ? o1 : o2;
  console.log(x + rest.y);
  return 0;
}
