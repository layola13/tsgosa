interface P { x: number; y: number; z: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2, z: 3 };
  const { x, ...rest } = o;
  console.log(x + rest.y + rest.z);
  return 0;
}
