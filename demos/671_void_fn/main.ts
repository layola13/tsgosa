function f(): void {
  console.log(1);
}
function g(): void {
  void f();
  console.log(2);
}
g();
