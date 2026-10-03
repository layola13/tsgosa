interface Q {
  x: i32;
}
function main(): i32 {
  const o: Q = { x: 1 };
  console.log("x" in o ? 1 : 0);
  return 0;
}
