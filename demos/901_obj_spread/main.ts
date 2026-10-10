interface P { x: i32; y: i32; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const p: P = { ...o, y: 3 };
  console.log(p.y);
  return 0;
}
