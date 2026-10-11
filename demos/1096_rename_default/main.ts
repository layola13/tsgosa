interface Q { x: i32; y: i32; }
function main(): i32 {
  const o: Q = { x: 3, y: 4 };
  const { x: a, y: b = 9 } = o;
  console.log(a + b);
  return 0;
}
