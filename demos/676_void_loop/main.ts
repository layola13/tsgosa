function f(): void {
  console.log(7);
}
for (let i = 0; i < 2; i = i + 1) {
  void f();
}
console.log(1);
