interface O { a: i32; }
function main(): i32 {
  const o: O = { a: 1 };
  console.log(Object.keys(o).length);
  return 0;
}
