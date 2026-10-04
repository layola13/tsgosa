interface P { x: number; y: number; }
function get(p: P): number {
  return p.x;
}
function main(): i32 {
  const o: P = { x: 40, y: 2 };
  const { x } = o;
  return x;
}
console.log(main());
