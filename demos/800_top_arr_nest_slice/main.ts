const N = [[1, 2], [3, 4]];
function main(): i32 {
  const c = N.slice(1);
  console.log(c.length + c[0][0]);
  return 0;
}
