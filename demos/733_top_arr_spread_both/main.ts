const A = [1], B = [2, 3];
function main(): i32 {
  const c = [...A, ...B];
  console.log(c.length + c[2]);
  return 0;
}
