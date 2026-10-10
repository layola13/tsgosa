interface P { x: i32; y: i32; }
function main(): i32 {
  const a: P = { x: 1, y: 2 };
  const b: P = { ...a, y: 5 };
  console.log(b.x);
  console.log(b.y);
  console.log(Object.keys(b).length);
  return 0;
}
