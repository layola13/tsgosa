enum E { A, B }
function f(): i32 { return E.B; }
function main(): i32 {
  console.log(f());
  return 0;
}
