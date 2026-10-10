const A = [1, 2, 3];
function main(): i32 {
  let s = 0;
  A.forEach((x) => {
    s = s + x;
  });
  console.log(s);
  return 0;
}
