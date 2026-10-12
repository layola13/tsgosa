interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  let t = 0;
  for (let i = 0; i < 3; i++) { t = t + rest.y; }
  console.log(t);
  return 0;
}
