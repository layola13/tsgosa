interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const a: number[] = [10, 20];
  let s = 0;
  a.forEach(() => { const { x, ...rest } = o; s = s + x + rest.y; });
  console.log(s);
  return 0;
}
