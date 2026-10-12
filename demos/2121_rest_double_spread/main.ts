interface P { x: number; y: number; z: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2, z: 3 };
  const { x, ...r1 } = o;
  const { y, ...r2 } = o;
  const c: P = { ...r1, ...r2, x: 0 };
  console.log(c.x + c.y + c.z);
  return 0;
}
