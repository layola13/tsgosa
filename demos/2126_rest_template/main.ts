interface P { x: number; s: string; }
function main(): i32 {
  const o: P = { x: 1, s: "hi" };
  const { x, ...rest } = o;
  console.log(`${rest.s}!`);
  return 0;
}
