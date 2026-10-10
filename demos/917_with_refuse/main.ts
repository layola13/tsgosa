interface O { x: i32; }
function main(): i32 {
  const o: O = { x: 1 };
  with (o) {
    console.log(x);
  }
  return 0;
}
