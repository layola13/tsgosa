interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 5, y: 6 };
  const { x, ...rest } = o;
  console.log(x > 3 ? rest.y : 0);
  return 0;
}
