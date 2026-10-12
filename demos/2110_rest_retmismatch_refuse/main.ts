interface P { x: number; y: number; }
function mk(): P {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  return rest;
}
function main(): i32 {
  const r: P = mk();
  console.log(r.y);
  return 0;
}
