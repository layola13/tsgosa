interface Pt { x: i32; y: i32; }
function main(): i32 {
  const p: Pt = { x: 3, y: 4 };
  console.log(p.x * p.x + p.y * p.y);
  return 0;
}
