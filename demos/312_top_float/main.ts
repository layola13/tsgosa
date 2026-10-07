const PI2 = 6.5;
namespace N {
  export const K = 2.5;
}
function main(): i32 {
  let s: i32 = 0;
  if (PI2 > 6) { s = s + 1; }
  if (N.K > 2) { s = s + 10; }
  console.log(s);
  return s;
}
console.log(main());
