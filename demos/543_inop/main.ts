interface O { k: i32; }
function main(): i32 {
  const o: O = { k: 1 };
  console.log("k" in o ? 1 : 0);
  return 0;
}
