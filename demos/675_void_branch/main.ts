function f(): void {
  console.log(7);
}
const b = true;
if (b) {
  void f();
}
console.log(1);
