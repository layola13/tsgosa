const A = [1, 2];
function main(): i32 {
  const b = [...A, 4];
  console.log(b.length + b[2]);
  return 0;
}
