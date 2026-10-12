interface P { x: number; y: number; z: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2, z: 3 };
  const { x, ...rest } = o;
  const c: P = { ...rest, x: 9 };
  console.log(c.x + c.y + c.z);
  return 0;
}
