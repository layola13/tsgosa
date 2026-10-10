interface B { f: boolean; n: i32 }
const O: B = {f: true, n: 3};
function main(): i32 {
  console.log(O.n);
  console.log(O.n + O.n);
  return 0;
}
