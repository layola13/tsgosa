interface P { x: i32; }
function main(): i32 {
  const o: P = { x: 1 };
  console.log("x" in o ? 1 : 0);
  console.log("y" in o ? 1 : 0);
  return 0;
}
