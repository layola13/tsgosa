interface P { x: number; y: number; z: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2, z: 3 };
  const { x, ...r1 } = o;
  const { y, ...r2 } = r1;
  console.log(r2.z);
  return 0;
}
