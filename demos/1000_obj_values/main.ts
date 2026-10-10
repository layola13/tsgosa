interface P { x: i32; y: i32; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  console.log(Object.values(o).length);
  return 0;
}
